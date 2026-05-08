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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

func (m *Manager) registerApplyManifest() {
	tool := mcp.NewTool(m.toolName("apply_manifest"),
		mcp.WithDescription("Applies a YAML/JSON manifest (create or update)"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("manifest", mcp.Required(), mcp.Description("YAML or JSON manifest to apply")),
		mcp.WithString("namespace", mcp.Description("Namespace override (optional)")),
	)
	m.mcpServer.AddTool(tool, m.handleApplyManifest)
}

func (m *Manager) handleApplyManifest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	// Try to create, if exists then update
	var result *unstructured.Unstructured
	if namespace != "" {
		result, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
		if err != nil && strings.Contains(err.Error(), "already exists") {
			result, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
		}
	} else {
		result, err = client.DynamicClient.Resource(gvr).Create(ctx, obj, metav1.CreateOptions{})
		if err != nil && strings.Contains(err.Error(), "already exists") {
			result, err = client.DynamicClient.Resource(gvr).Update(ctx, obj, metav1.UpdateOptions{})
		}
	}

	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, err := objectToYAML(result)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully applied %s/%s in namespace %s\n\n%s", gvk.Kind, obj.GetName(), namespace, yamlOutput)), nil
}

func (m *Manager) registerPatchResource() {
	tool := mcp.NewTool(m.toolName("patch_resource"),
		mcp.WithDescription("Patches an existing Kubernetes resource"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense, lowercase plural (e.g., 'pods', 'deployments'). NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource instance name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithString("patch_type", mcp.Required(), mcp.Description("Patch type: 'strategic', 'merge', or 'json'")),
		mcp.WithString("patch", mcp.Required(), mcp.Description("Patch content (YAML or JSON)")),
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
		mcp.WithDescription("Deletes a Kubernetes resource"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense, lowercase plural (e.g., 'pods', 'deployments'). NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource instance name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithNumber("grace_period_seconds", mcp.Description("Grace period in seconds")),
		mcp.WithString("propagation_policy", mcp.Description("Deletion propagation policy: 'Orphan', 'Background', 'Foreground'")),
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

	deleteOpts := getDeleteOptions(args)

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
		mcp.WithDescription("Deletes multiple Kubernetes resources matching selectors"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense, lowercase plural (e.g., 'pods', 'deployments'). NOT the Kind.")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithString("label_selector", mcp.Description("Label selector")),
		mcp.WithString("field_selector", mcp.Description("Field selector")),
		mcp.WithNumber("grace_period_seconds", mcp.Description("Grace period in seconds")),
	)
	m.mcpServer.AddTool(tool, m.handleDeleteResources)
}

func (m *Manager) handleDeleteResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	namespace, _ := args["namespace"].(string)
	labelSelector, _ := args["label_selector"].(string)
	fieldSelector, _ := args["field_selector"].(string)
	gvr := gvrFromArgs(args)
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
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
	deleteOpts := getDeleteOptions(args)

	if namespace != "" {
		err = client.DynamicClient.Resource(gvr).Namespace(namespace).DeleteCollection(ctx, deleteOpts, listOpts)
	} else {
		err = client.DynamicClient.Resource(gvr).DeleteCollection(ctx, deleteOpts, listOpts)
	}

	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully deleted %s resources matching selector in namespace %s", gvr.Resource, namespace)), nil
}
