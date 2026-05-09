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
	"encoding/json"
	"fmt"
	"strings"

	"kubernetes-mcp/internal/authorization"

	"github.com/mark3labs/mcp-go/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

func (m *Manager) registerApplyManifest() {
	tool := mcp.NewTool(m.toolName("apply_manifest"),
		mcp.WithDescription(`Create-or-update (upsert) a Kubernetes resource from a YAML or JSON manifest.

Behaviour: tries to Create the resource; if it already exists, falls back
to Update. The resource type is detected automatically from the manifest's
'apiVersion' / 'kind', resolved against the cluster's discovery API via the
RESTMapper, so CRDs and irregular plurals (StorageClass, NetworkPolicy, ...)
work transparently.

Limitations:
  - Single-document manifests only. Multi-document YAML separated by '---'
    is NOT supported; pass each document in a separate call.
  - This is a 'replace'-style update, not server-side strategic merge.
    For surgical changes prefer 'patch_resource'.

Use 'diff_manifest' first if you want to preview the change without applying it.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("manifest", mcp.Required(), mcp.Description("A single Kubernetes manifest in YAML or JSON. Must include 'apiVersion', 'kind' and 'metadata.name'. For namespaced kinds either set 'metadata.namespace' here or pass the 'namespace' argument.")),
		mcp.WithString("namespace", mcp.Description("Namespace override. If set, takes precedence over 'metadata.namespace' from the manifest. Ignored for cluster-scoped kinds.")),
	)
	m.mcpServer.AddTool(tool, m.handleApplyManifest)
}

func (m *Manager) handleApplyManifest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	manifest, _ := args["manifest"].(string)
	namespaceOverride, _ := args["namespace"].(string)

	// Reject multi-document YAML explicitly. sigs.k8s.io/yaml.Unmarshal would
	// silently keep only the first document, which masks bugs in callers.
	if isMultiDocumentYAML(manifest) {
		return errorResult(fmt.Errorf("multi-document YAML is not supported; submit one document per call (separators '---' detected)")), nil
	}

	// Parse manifest
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(manifest), &obj.Object); err != nil {
		return errorResult(fmt.Errorf("failed to parse manifest: %w", err)), nil
	}
	if len(obj.Object) == 0 {
		return errorResult(fmt.Errorf("manifest is empty")), nil
	}

	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return errorResult(fmt.Errorf("manifest is missing 'kind'")), nil
	}
	if obj.GetName() == "" {
		return errorResult(fmt.Errorf("manifest is missing 'metadata.name'")), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	// Resolve GVR + namespaced flag from the cluster discovery via RESTMapper
	gvr, namespaced, err := m.resolveGVRForGVK(client, gvk)
	if err != nil {
		return errorResult(err), nil
	}

	namespace := obj.GetNamespace()
	if namespaceOverride != "" {
		namespace = namespaceOverride
		obj.SetNamespace(namespace)
	}
	if !namespaced {
		namespace = ""
		obj.SetNamespace("")
	}

	// Check authorization
	if err := m.checkAuthorization(request, "apply_manifest", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
		Name:     obj.GetName(),
	}); err != nil {
		return errorResult(err), nil
	}

	if namespace != "" && !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	// Try to create. If the resource already exists, do a proper read-modify-
	// write update: GET the live object, copy server-managed immutable fields
	// (resourceVersion, clusterIP, ...), then Update.
	resourceClient := client.DynamicClient.Resource(gvr)
	var nsClient dynamicResource = resourceClient
	if namespace != "" {
		nsClient = resourceClient.Namespace(namespace)
	}

	created, err := nsClient.Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		yamlOutput, _ := objectToYAML(created)
		return successResult(fmt.Sprintf("Successfully created %s/%s in namespace %s\n\n%s", gvk.Kind, obj.GetName(), namespace, yamlOutput)), nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return errorResult(err), nil
	}

	// Already exists -> Update path. Need the live object for resourceVersion
	// and to preserve server-set immutable fields.
	live, getErr := nsClient.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if getErr != nil {
		return errorResult(fmt.Errorf("resource exists but could not be fetched for update: %w", getErr)), nil
	}

	mergeImmutableFields(obj, live, gvk)
	obj.SetResourceVersion(live.GetResourceVersion())

	updated, err := nsClient.Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, _ := objectToYAML(updated)
	return successResult(fmt.Sprintf("Successfully updated %s/%s in namespace %s\n\n%s", gvk.Kind, obj.GetName(), namespace, yamlOutput)), nil
}

// dynamicResource is the minimal subset of dynamic.ResourceInterface we use,
// allowing the same code path for namespaced and cluster-scoped resources.
type dynamicResource interface {
	Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error)
	Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error)
	Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error)
}

// isMultiDocumentYAML reports whether the input contains more than one YAML
// document by looking for a '---' separator on its own line.
func isMultiDocumentYAML(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "---" {
			// '---' at the very start is just a document marker, not a separator.
			// A separator implies a *second* document follows, so we need a non-empty
			// document already seen before it.
			return hasNonEmptyContentBefore(s, line)
		}
	}
	return false
}

// hasNonEmptyContentBefore reports whether there is any non-comment,
// non-blank line before the given separator line in s.
func hasNonEmptyContentBefore(s, sep string) bool {
	for _, line := range strings.Split(s, "\n") {
		if line == sep {
			return false
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		return true
	}
	return false
}

// mergeImmutableFields copies server-managed immutable fields from the live
// object into the desired object. Without this, the Update would either fail
// (Conflict on missing resourceVersion) or wipe out server-assigned values
// (Service clusterIP, PVC volumeName, ...).
func mergeImmutableFields(desired, live *unstructured.Unstructured, gvk schema.GroupVersionKind) {
	switch {
	case gvk.Group == "" && gvk.Kind == "Service":
		// clusterIP / clusterIPs are immutable once assigned.
		preserveString(desired, live, "spec", "clusterIP")
		preserveSlice(desired, live, "spec", "clusterIPs")
		preserveSlice(desired, live, "spec", "ipFamilies")
		preserveString(desired, live, "spec", "ipFamilyPolicy")
	case gvk.Group == "" && gvk.Kind == "PersistentVolumeClaim":
		preserveString(desired, live, "spec", "volumeName")
		preserveString(desired, live, "spec", "volumeMode")
	}
}

// preserveString copies a string field from live -> desired only if desired
// does not already have one.
func preserveString(desired, live *unstructured.Unstructured, fields ...string) {
	if cur, found, _ := unstructured.NestedString(desired.Object, fields...); found && cur != "" {
		return
	}
	if v, found, _ := unstructured.NestedString(live.Object, fields...); found {
		_ = unstructured.SetNestedField(desired.Object, v, fields...)
	}
}

// preserveSlice copies a string slice from live -> desired only if desired
// does not already have one.
func preserveSlice(desired, live *unstructured.Unstructured, fields ...string) {
	if cur, found, _ := unstructured.NestedSlice(desired.Object, fields...); found && len(cur) > 0 {
		return
	}
	if v, found, _ := unstructured.NestedSlice(live.Object, fields...); found {
		_ = unstructured.SetNestedSlice(desired.Object, v, fields...)
	}
}

func (m *Manager) registerPatchResource() {
	tool := mcp.NewTool(m.toolName("patch_resource"),
		mcp.WithDescription(`Apply a partial change to an existing Kubernetes resource.

Prefer this over 'apply_manifest' when you only want to change a few fields
(image tag, replica count not via scale, annotation, ...).

Choose 'patch_type' carefully:
  - 'strategic': Strategic Merge Patch. Works only on built-in Kubernetes
    types and understands list-merge semantics (e.g. patching a single
    container by name). The default for most kubectl operations.
  - 'merge': RFC 7396 JSON Merge Patch. Works on any resource including
    CRDs. Replaces lists entirely (does not merge them by key).
  - 'json': RFC 6902 JSON Patch. An array of operations like
    [{"op":"replace","path":"/spec/replicas","value":3}]. Most precise.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Empty string \"\" for the core API.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, e.g. 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense: lowercase plural ('pods', 'deployments'). NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the resource instance to patch.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the resource lives. Required for namespaced resources.")),
		mcp.WithString("patch_type", mcp.Required(), mcp.Description("'strategic' for Strategic Merge Patch (built-in types only), 'merge' for RFC 7396 JSON Merge Patch (works on CRDs), or 'json' for RFC 6902 JSON Patch operations.")),
		mcp.WithString("patch", mcp.Required(), mcp.Description("Patch payload. YAML and JSON are both accepted. For 'json' patch_type the payload must be a JSON array of operations.")),
	)
	m.mcpServer.AddTool(tool, m.handlePatchResource)
}

func (m *Manager) handlePatchResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	patchTypeStr, _ := args["patch_type"].(string)
	patchData, _ := args["patch"].(string)
	gvr := gvrFromArgs(args)
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "patch_resource", k8sContext, namespace, authorization.ResourceInfo{
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

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	// Convert patch type
	var patchType types.PatchType
	switch strings.ToLower(patchTypeStr) {
	case "strategic":
		patchType = types.StrategicMergePatchType
	case "merge":
		patchType = types.MergePatchType
	case "json":
		patchType = types.JSONPatchType
	default:
		return errorResult(fmt.Errorf("invalid patch type: %s", patchTypeStr)), nil
	}

	// Validate patch is non-empty (avoid panic in subsequent indexing).
	if strings.TrimSpace(patchData) == "" {
		return errorResult(fmt.Errorf("patch is empty")), nil
	}

	// Convert YAML patch to JSON if needed
	var patchBytes []byte
	if strings.TrimSpace(patchData)[0] == '{' || strings.TrimSpace(patchData)[0] == '[' {
		patchBytes = []byte(patchData)
	} else {
		var patchObj any
		if err := yaml.Unmarshal([]byte(patchData), &patchObj); err != nil {
			return errorResult(fmt.Errorf("failed to parse patch: %w", err)), nil
		}
		patchBytes, err = json.Marshal(patchObj)
		if err != nil {
			return errorResult(fmt.Errorf("failed to convert patch to JSON: %w", err)), nil
		}
	}

	var result *unstructured.Unstructured
	if namespace != "" {
		result, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, patchType, patchBytes, metav1.PatchOptions{})
	} else {
		result, err = client.DynamicClient.Resource(gvr).Patch(ctx, name, patchType, patchBytes, metav1.PatchOptions{})
	}

	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, err := objectToYAML(result)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully patched %s/%s\n\n%s", gvr.Resource, name, yamlOutput)), nil
}

func (m *Manager) registerDeleteResource() {
	tool := mcp.NewTool(m.toolName("delete_resource"),
		mcp.WithDescription(`Delete ONE Kubernetes resource by name.

DESTRUCTIVE. Cannot be undone (the API server has no trash bin).
Verify with 'get_resource' first if you have any doubt.

For deleting many objects at once with a selector use 'delete_resources'
instead — but be even more careful.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Empty string \"\" for the core API.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, e.g. 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense: lowercase plural ('pods', 'deployments'). NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the resource instance to delete.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the resource lives. Required for namespaced resources.")),
		mcp.WithNumber("grace_period_seconds", mcp.Description("Seconds before forced termination. 0 = delete immediately (forceful, may leak resources). Omit to use the resource's default (30s for Pods).")),
		mcp.WithString("propagation_policy", mcp.Description("How to handle dependents. 'Background' (default for most kinds): API returns immediately, dependents deleted asynchronously. 'Foreground': blocks until dependents are gone. 'Orphan': leaves dependents alive (e.g. delete a Deployment but keep its Pods).")),
	)
	m.mcpServer.AddTool(tool, m.handleDeleteResource)
}

func (m *Manager) handleDeleteResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	gvr := gvrFromArgs(args)
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "delete_resource", k8sContext, namespace, authorization.ResourceInfo{
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

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	deleteOpts, err := getDeleteOptions(args)
	if err != nil {
		return errorResult(err), nil
	}

	if namespace != "" {
		err = client.DynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, deleteOpts)
	} else {
		err = client.DynamicClient.Resource(gvr).Delete(ctx, name, deleteOpts)
	}

	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully deleted %s/%s in namespace %s", gvr.Resource, name, namespace)), nil
}

func (m *Manager) registerDeleteResources() {
	tool := mcp.NewTool(m.toolName("delete_resources"),
		mcp.WithDescription(`Delete MANY resources at once matching a label and/or field selector.

VERY DESTRUCTIVE. Always run 'list_resources' with the same selector first
to confirm exactly which objects will be deleted. At least one selector
('label_selector' or 'field_selector') is REQUIRED to avoid accidentally
wiping a whole namespace.

Safety checks enforced by this tool:
  - 'namespace' must be set unless 'all_namespaces=true' is passed
    explicitly (this barrier prevents accidental cross-namespace deletes).
  - The total number of matched resources is capped by the server's
    'kubernetes.tools.bulk_operations.max_resources_per_operation' setting
    (default 100); the call is rejected if the selector matches more.

For a single named resource use 'delete_resource'.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Empty string \"\" for the core API.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, e.g. 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense: lowercase plural ('pods', 'deployments'). NOT the Kind.")),
		mcp.WithString("namespace", mcp.Description("Namespace to scope the deletion to. Required unless 'all_namespaces=true' is set.")),
		mcp.WithBoolean("all_namespaces", mcp.Description("If true, deletion is applied across ALL namespaces. Required to opt in to cross-namespace deletes; mutually exclusive with 'namespace'.")),
		mcp.WithString("label_selector", mcp.Description("Kubernetes label selector. Examples: 'app=nginx', 'temp=true', 'tier in (frontend,backend)'. Required if 'field_selector' is empty.")),
		mcp.WithString("field_selector", mcp.Description("Kubernetes field selector. Example: 'status.phase=Failed'. Required if 'label_selector' is empty.")),
		mcp.WithNumber("grace_period_seconds", mcp.Description("Seconds before forced termination. 0 = delete immediately. Omit to use the resource's default.")),
	)
	m.mcpServer.AddTool(tool, m.handleDeleteResources)
}

func (m *Manager) handleDeleteResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	namespace, _ := args["namespace"].(string)
	allNamespaces, _ := args["all_namespaces"].(bool)
	labelSelector, _ := args["label_selector"].(string)
	fieldSelector, _ := args["field_selector"].(string)
	gvr := gvrFromArgs(args)
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}

	// Cross-namespace barrier.
	if allNamespaces && namespace != "" {
		return errorResult(fmt.Errorf("'namespace' and 'all_namespaces=true' are mutually exclusive")), nil
	}
	if !allNamespaces && namespace == "" {
		return errorResult(fmt.Errorf("'namespace' is required unless 'all_namespaces=true' is passed explicitly")), nil
	}

	// Require at least one selector for safety
	if labelSelector == "" && fieldSelector == "" {
		return errorResult(fmt.Errorf("at least one selector (label_selector or field_selector) is required")), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "delete_resources", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
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

	listOpts := getListOptions(args)
	deleteOpts, err := getDeleteOptions(args)
	if err != nil {
		return errorResult(err), nil
	}

	// Pre-list to enforce the bulk-operations cap. Avoids "delete and pray".
	maxBulk := m.config.Kubernetes.Tools.BulkOperations.MaxResourcesPerOperation
	if maxBulk <= 0 {
		maxBulk = 100
	}

	var preList *unstructured.UnstructuredList
	if namespace != "" {
		preList, err = client.DynamicClient.Resource(gvr).Namespace(namespace).List(ctx, listOpts)
	} else {
		preList, err = client.DynamicClient.Resource(gvr).List(ctx, listOpts)
	}
	if err != nil {
		return errorResult(fmt.Errorf("could not pre-list resources before delete: %w", err)), nil
	}
	matched := len(preList.Items)
	if matched == 0 {
		return successResult(fmt.Sprintf("No %s matched the selector; nothing to delete", gvr.Resource)), nil
	}
	if matched > maxBulk {
		return errorResult(fmt.Errorf("selector matched %d resources, which exceeds the configured cap of %d (kubernetes.tools.bulk_operations.max_resources_per_operation); refine the selector or raise the cap", matched, maxBulk)), nil
	}

	if namespace != "" {
		err = client.DynamicClient.Resource(gvr).Namespace(namespace).DeleteCollection(ctx, deleteOpts, listOpts)
	} else {
		err = client.DynamicClient.Resource(gvr).DeleteCollection(ctx, deleteOpts, listOpts)
	}

	if err != nil {
		return errorResult(err), nil
	}

	scope := "namespace " + namespace
	if allNamespaces {
		scope = "all namespaces"
	}
	return successResult(fmt.Sprintf("Successfully deleted %d %s matching selector in %s", matched, gvr.Resource, scope)), nil
}
