/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package k8stools

import (
	"context"
	"fmt"
	"strings"

	"kubernetes-mcp/internal/authorization"

	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func (m *Manager) registerDiffManifest() {
	tool := mcp.NewTool(m.toolName("diff_manifest"),
		mcp.WithDescription(`Preview the changes that 'apply_manifest' would make, WITHOUT applying them.

Compares the desired manifest against the current cluster state and reports
field-level additions / removals / modifications. Skips auto-managed fields
('resourceVersion', 'uid', 'creationTimestamp', 'managedFields', 'status').

If the resource does not yet exist, the tool reports that it would be CREATED.

The resource type is resolved from the manifest's 'apiVersion' / 'kind' via
the cluster's RESTMapper, so CRDs and irregular plurals work transparently.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("manifest", mcp.Required(), mcp.Description("A single Kubernetes manifest in YAML or JSON. Multi-document YAML is NOT supported.")),
		mcp.WithString("namespace", mcp.Description("Namespace override. If set, takes precedence over 'metadata.namespace' from the manifest. Ignored for cluster-scoped kinds.")),
	)
	m.mcpServer.AddTool(tool, m.handleDiffManifest)
}

func (m *Manager) handleDiffManifest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	manifest, _ := args["manifest"].(string)
	namespaceOverride, _ := args["namespace"].(string)

	// Parse manifest
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(manifest), &obj.Object); err != nil {
		return errorResult(fmt.Errorf("failed to parse manifest: %w", err)), nil
	}

	gvk := obj.GroupVersionKind()
	name := obj.GetName()

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	// Resolve GVR + namespaced flag from cluster discovery via RESTMapper
	gvr, namespaced, err := m.resolveGVRForGVK(client, gvk)
	if err != nil {
		return errorResult(err), nil
	}

	namespace := obj.GetNamespace()
	if namespaceOverride != "" {
		namespace = namespaceOverride
	}
	if !namespaced {
		namespace = ""
	}

	// Check authorization
	if err := m.checkAuthorization(request, "diff_manifest", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
		Name:     name,
	}); err != nil {
		return errorResult(err), nil
	}

	if namespace != "" && !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	// Get current resource from cluster
	var current *unstructured.Unstructured
	if namespace != "" {
		current, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		current, err = client.DynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return successResult(fmt.Sprintf("Resource %s/%s does not exist in namespace %s\nThis manifest would CREATE a new resource.", gvk.Kind, name, namespace)), nil
		}
		return errorResult(err), nil
	}

	// Compare the two
	currentYAML, err := objectToYAML(current.Object)
	if err != nil {
		return errorResult(err), nil
	}

	desiredYAML, err := objectToYAML(obj.Object)
	if err != nil {
		return errorResult(err), nil
	}

	// Simple diff - compare key fields
	diff := compareObjects(current.Object, obj.Object, "")

	if len(diff) == 0 {
		return successResult(fmt.Sprintf("No changes detected for %s/%s in namespace %s", gvk.Kind, name, namespace)), nil
	}

	output := fmt.Sprintf("Diff for %s/%s in namespace %s:\n\n", gvk.Kind, name, namespace)
	output += "Changes:\n"
	for _, d := range diff {
		output += fmt.Sprintf("  %s\n", d)
	}
	output += "\n--- Current ---\n" + currentYAML
	output += "\n--- Desired ---\n" + desiredYAML

	return successResult(output), nil
}

// compareObjects compares two maps and returns a list of differences.
// It applies a "strip" pass to both sides to ignore server-managed fields
// that produce false positives (last-applied-configuration, finalizers,
// cluster-assigned IPs, etc.) before walking the structure.
func compareObjects(current, desired map[string]any, path string) []string {
	if path == "" {
		current = stripServerManagedFields(current)
		desired = stripServerManagedFields(desired)
	}
	var diffs []string

	for key, desiredVal := range desired {
		currentPath := key
		if path != "" {
			currentPath = path + "." + key
		}

		currentVal, exists := current[key]
		if !exists {
			diffs = append(diffs, fmt.Sprintf("+ %s: %v", currentPath, summarizeValue(desiredVal)))
			continue
		}

		// Compare values
		switch dv := desiredVal.(type) {
		case map[string]any:
			if cv, ok := currentVal.(map[string]any); ok {
				diffs = append(diffs, compareObjects(cv, dv, currentPath)...)
			} else {
				diffs = append(diffs, fmt.Sprintf("~ %s: type changed", currentPath))
			}
		case []any:
			if cv, ok := currentVal.([]any); ok {
				if !slicesEqualNormalized(cv, dv) {
					diffs = append(diffs, fmt.Sprintf("~ %s: array changed", currentPath))
				}
			} else {
				diffs = append(diffs, fmt.Sprintf("~ %s: type changed", currentPath))
			}
		default:
			if currentVal != desiredVal {
				diffs = append(diffs, fmt.Sprintf("~ %s: %v -> %v", currentPath, summarizeValue(currentVal), summarizeValue(desiredVal)))
			}
		}
	}

	// Check for removed fields
	for key := range current {
		currentPath := key
		if path != "" {
			currentPath = path + "." + key
		}
		if _, exists := desired[key]; !exists {
			diffs = append(diffs, fmt.Sprintf("- %s: %v", currentPath, summarizeValue(current[key])))
		}
	}

	return diffs
}

// stripServerManagedFields removes fields that the API server adds or owns
// from a deep copy of the input. Returning a copy avoids mutating the
// caller's map. Only top-level structural fields are walked; nested
// 'metadata' keys are removed in place.
func stripServerManagedFields(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	// Drop the entire status subtree — it's controller-owned, never relevant
	// for what the user is about to apply.
	delete(out, "status")

	if md, ok := out["metadata"].(map[string]any); ok {
		mdCopy := make(map[string]any, len(md))
		for k, v := range md {
			mdCopy[k] = v
		}
		// Server-managed metadata fields. Removing them on BOTH sides means
		// they cannot show up as diffs.
		for _, f := range []string{
			"resourceVersion",
			"uid",
			"creationTimestamp",
			"deletionTimestamp",
			"deletionGracePeriodSeconds",
			"generation",
			"managedFields",
			"selfLink",
			"finalizers",
			"ownerReferences",
		} {
			delete(mdCopy, f)
		}
		// last-applied-configuration is added by `kubectl apply`. It always
		// represents the previous version, never the current one — strip it.
		if ann, ok := mdCopy["annotations"].(map[string]any); ok {
			annCopy := make(map[string]any, len(ann))
			for k, v := range ann {
				if k == "kubectl.kubernetes.io/last-applied-configuration" {
					continue
				}
				annCopy[k] = v
			}
			if len(annCopy) == 0 {
				delete(mdCopy, "annotations")
			} else {
				mdCopy["annotations"] = annCopy
			}
		}
		out["metadata"] = mdCopy
	}

	// Service: clusterIP / clusterIPs / ipFamilies are server-assigned the
	// first time and immutable thereafter. Stripping them from current avoids
	// "removed" diffs when the user submits a manifest without them.
	kind, _ := out["kind"].(string)
	if kind == "Service" {
		if spec, ok := out["spec"].(map[string]any); ok {
			specCopy := make(map[string]any, len(spec))
			for k, v := range spec {
				specCopy[k] = v
			}
			delete(specCopy, "clusterIP")
			delete(specCopy, "clusterIPs")
			delete(specCopy, "ipFamilies")
			delete(specCopy, "ipFamilyPolicy")
			out["spec"] = specCopy
		}
	}
	if kind == "PersistentVolumeClaim" {
		if spec, ok := out["spec"].(map[string]any); ok {
			specCopy := make(map[string]any, len(spec))
			for k, v := range spec {
				specCopy[k] = v
			}
			delete(specCopy, "volumeName")
			out["spec"] = specCopy
		}
	}

	return out
}

func summarizeValue(v any) string {
	switch val := v.(type) {
	case string:
		if len(val) > 50 {
			return fmt.Sprintf("%q...", val[:50])
		}
		return fmt.Sprintf("%q", val)
	case map[string]any:
		return fmt.Sprintf("{...%d keys}", len(val))
	case []any:
		return fmt.Sprintf("[...%d items]", len(val))
	default:
		return fmt.Sprintf("%v", val)
	}
}

// slicesEqualNormalized compares two slices for content equality, ignoring
// order when all elements are scalars or maps with a 'name' key (a common
// pattern in container ports / env / volumeMounts). For mixed or nested-list
// content we fall back to ordered comparison.
func slicesEqualNormalized(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	if isUnorderedScalar(a) && isUnorderedScalar(b) {
		return scalarSetEqual(a, b)
	}
	if isNamedMapList(a) && isNamedMapList(b) {
		return namedMapListEqual(a, b)
	}
	// Fallback: ordered, stringified compare.
	for i := range a {
		if fmt.Sprintf("%v", a[i]) != fmt.Sprintf("%v", b[i]) {
			return false
		}
	}
	return true
}

func isUnorderedScalar(s []any) bool {
	for _, v := range s {
		switch v.(type) {
		case string, float64, int64, int, bool, nil:
		default:
			return false
		}
	}
	return true
}

func scalarSetEqual(a, b []any) bool {
	count := func(in []any) map[string]int {
		m := make(map[string]int, len(in))
		for _, v := range in {
			m[fmt.Sprintf("%v", v)]++
		}
		return m
	}
	ca, cb := count(a), count(b)
	if len(ca) != len(cb) {
		return false
	}
	for k, v := range ca {
		if cb[k] != v {
			return false
		}
	}
	return true
}

func isNamedMapList(s []any) bool {
	for _, v := range s {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if _, hasName := m["name"]; !hasName {
			return false
		}
	}
	return true
}

func namedMapListEqual(a, b []any) bool {
	index := func(in []any) map[string]any {
		out := make(map[string]any, len(in))
		for _, v := range in {
			m := v.(map[string]any)
			name, _ := m["name"].(string)
			out[name] = m
		}
		return out
	}
	ia, ib := index(a), index(b)
	if len(ia) != len(ib) {
		return false
	}
	for k, va := range ia {
		vb, ok := ib[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", va) != fmt.Sprintf("%v", vb) {
			return false
		}
	}
	return true
}
