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
	tool := mcp.NewTool("diff_manifest",
		mcp.WithDescription("Compares a manifest with the current cluster state"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("manifest", mcp.Required(), mcp.Description("YAML or JSON manifest to compare")),
		mcp.WithString("namespace", mcp.Description("Namespace override (optional)")),
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
	namespace := obj.GetNamespace()
	if namespaceOverride != "" {
		namespace = namespaceOverride
	}
	name := obj.GetName()

	// Check authorization
	if err := m.checkAuthorization(request, "diff_manifest", k8sContext, namespace, authorization.ResourceInfo{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind,
		Name:    name,
	}); err != nil {
		return errorResult(err), nil
	}

	if namespace != "" && !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	gvr := getGVR(gvk.Group, gvk.Version, gvk.Kind)

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

// compareObjects compares two maps and returns a list of differences
func compareObjects(current, desired map[string]any, path string) []string {
	var diffs []string

	// Skip metadata fields that are auto-managed
	skipFields := map[string]bool{
		"metadata.resourceVersion":   true,
		"metadata.uid":               true,
		"metadata.creationTimestamp": true,
		"metadata.generation":        true,
		"metadata.managedFields":     true,
		"metadata.selfLink":          true,
		"status":                     true,
	}

	for key, desiredVal := range desired {
		currentPath := key
		if path != "" {
			currentPath = path + "." + key
		}

		if skipFields[currentPath] {
			continue
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
				if !slicesEqual(cv, dv) {
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

		if skipFields[currentPath] {
			continue
		}

		if _, exists := desired[key]; !exists {
			diffs = append(diffs, fmt.Sprintf("- %s: %v", currentPath, summarizeValue(current[key])))
		}
	}

	return diffs
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

func slicesEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	// Simple comparison - for complex nested structures this would need more work
	for i := range a {
		if fmt.Sprintf("%v", a[i]) != fmt.Sprintf("%v", b[i]) {
			return false
		}
	}
	return true
}
