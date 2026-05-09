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

	"kubernetes-mcp/internal/authorization"

	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) registerGetResource() {
	tool := mcp.NewTool(m.toolName("get_resource"),
		mcp.WithDescription(`Fetch a single Kubernetes resource by name and return its full YAML.

Use this when you already know the exact name and type of the resource.
For listing many resources at once use 'list_resources'. For a richer
human-readable view including related events use 'describe_resource'.

The resource is addressed via GroupVersionResource (GVR), exactly as the
Kubernetes API expects. The plural lowercase form ('pods', 'deployments',
'ingresses', 'storageclasses', ...) is required, NOT the Kind.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context (see 'get_current_context').")),
		mcp.WithString("group", mcp.Description("API group. Empty string \"\" for the core API ('pods', 'configmaps', ...). Examples: 'apps', 'batch', 'networking.k8s.io'.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, e.g. 'v1', 'v1beta1', 'v2'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense: lowercase plural ('pods', 'deployments', 'ingresses', 'networkpolicies', 'storageclasses'). NOT the Kind ('Pod', 'Deployment', ...). Run 'list_api_resources' if unsure.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the specific resource instance to fetch.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the resource lives. Required for namespaced resources; ignored for cluster-scoped resources (Nodes, Namespaces, StorageClasses, ...).")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions (see https://mikefarah.gitbook.io/yq) applied in order to filter or transform the YAML output. Useful to keep the response small. Examples: '.metadata.name' (just the name), '.spec.containers[].image' (image list), '.status.podIP' (IP address), '{name: .metadata.name, ip: .status.podIP}' (custom shape).")),
	)
	m.mcpServer.AddTool(tool, m.handleGetResource)
}

func (m *Manager) handleGetResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	gvr := gvrFromArgs(args)
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "get_resource", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
		Name:     name,
	}); err != nil {
		return errorResult(err), nil
	}

	// Check namespace access
	if namespace != "" && !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	var result any
	if namespace != "" {
		result, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		result, err = client.DynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, err := objectToYAML(result)
	if err != nil {
		return errorResult(err), nil
	}

	// Apply yq expressions
	finalOutput, err := m.applyYQExpressions(yamlOutput, args)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(finalOutput), nil
}

func (m *Manager) registerListResources() {
	tool := mcp.NewTool(m.toolName("list_resources"),
		mcp.WithDescription(`List Kubernetes resources of a given type, optionally filtered by namespace,
labels and fields.

Prefer this over multiple 'get_resource' calls when scanning many objects.
Use 'yq_expressions' to project just the fields you need; large clusters can
return huge YAML and the model is not the right place to parse it.

Resources are addressed via GroupVersionResource (GVR). The plural lowercase
form ('pods', 'deployments', 'ingresses', ...) is required, NOT the Kind.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Empty string \"\" for the core API. Examples: 'apps', 'networking.k8s.io', 'batch'.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, e.g. 'v1', 'v1beta1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense: lowercase plural ('pods', 'deployments', 'ingresses'). NOT the Kind. Use 'list_api_resources' if unsure.")),
		mcp.WithString("namespace", mcp.Description("Namespace to scope the listing to. Empty string lists across ALL namespaces (subject to RBAC) and is ignored for cluster-scoped resources.")),
		mcp.WithString("label_selector", mcp.Description("Kubernetes label selector. Comma separates AND clauses. Examples: 'app=nginx', 'app=api,env!=prod', 'tier in (frontend,backend)'.")),
		mcp.WithString("field_selector", mcp.Description("Kubernetes field selector. Only a small set of fields is selectable per resource type (typically 'metadata.name', 'metadata.namespace', 'status.phase', 'spec.nodeName'). Examples: 'status.phase=Running', 'metadata.name=foo'.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of items to return. Integer >= 1. When the cluster has more matching items, the response includes a `metadata.continue` token; pass it back in 'continue_token' to fetch the next page. Omit for no limit (use only on small clusters).")),
		mcp.WithString("continue_token", mcp.Description("Continuation token returned by a previous call to fetch the next page. Pass alongside the same 'limit' and selectors.")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied in order to filter or transform the YAML output. The output is a List object so use '.items[]' to iterate. Examples: '.items[].metadata.name' (just names), '.items | length' (count), '.items[] | select(.status.phase == \"Running\") | .metadata.name' (filter+project), '.items[] | {name: .metadata.name, ip: .status.podIP}' (reshape).")),
	)
	m.mcpServer.AddTool(tool, m.handleListResources)
}

func (m *Manager) handleListResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	namespace, _ := args["namespace"].(string)
	gvr := gvrFromArgs(args)
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "list_resources", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
	}); err != nil {
		return errorResult(err), nil
	}

	// Check namespace access if specified
	if namespace != "" && !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	listOpts := getListOptions(args)

	var result any
	if namespace != "" {
		result, err = client.DynamicClient.Resource(gvr).Namespace(namespace).List(ctx, listOpts)
	} else {
		result, err = client.DynamicClient.Resource(gvr).List(ctx, listOpts)
	}

	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, err := objectToYAML(result)
	if err != nil {
		return errorResult(err), nil
	}

	// Apply yq expressions
	finalOutput, err := m.applyYQExpressions(yamlOutput, args)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(finalOutput), nil
}

func (m *Manager) registerDescribeResource() {
	tool := mcp.NewTool(m.toolName("describe_resource"),
		mcp.WithDescription(`Return a resource together with its related events, similar to 'kubectl describe'.

Best when troubleshooting: events explain why a Pod is Pending, why a
Deployment is not progressing, etc. The Kind used to filter events is
resolved automatically from the GVR via the cluster's RESTMapper, so the
caller only needs to provide the GVR.

Events are only included when 'namespace' is set (the events API is namespaced).`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Empty string \"\" for the core API.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, e.g. 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense: lowercase plural ('pods', 'deployments'). NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the specific resource instance.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the resource lives. Required for namespaced resources; ignored for cluster-scoped resources. Events are only included when this is set.")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied to the combined YAML (resource + events). The events are appended after a '---' separator. Examples: '.status.conditions' (just conditions), '.spec.containers[].image' (image list).")),
	)
	m.mcpServer.AddTool(tool, m.handleDescribeResource)
}

func (m *Manager) handleDescribeResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	gvr := gvrFromArgs(args)
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "describe_resource", k8sContext, namespace, authorization.ResourceInfo{
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

	// Get the resource
	var resource any
	if namespace != "" {
		resource, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		resource, err = client.DynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		return errorResult(err), nil
	}

	resourceYAML, err := objectToYAML(resource)
	if err != nil {
		return errorResult(err), nil
	}

	// Get related events. Resolve the Kind via the RESTMapper since events are
	// indexed by involvedObject.kind, not by resource. For cluster-scoped
	// resources we look across all namespaces (Node events live in 'default'
	// in stock Kubernetes, but other distributions vary).
	eventsOutput := ""
	kind, kindErr := m.resolveKindForGVR(client, gvr)
	if kindErr == nil && kind != "" {
		eventNS := namespace
		// For cluster-scoped resources (no namespace), search all namespaces.
		events, err := client.Clientset.CoreV1().Events(eventNS).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=%s", name, kind),
		})
		if err == nil && len(events.Items) > 0 {
			eventsYAML, _ := objectToYAML(events)
			eventsOutput = "\n---\n# Related Events\n" + eventsYAML
		}
	}

	combinedOutput := resourceYAML + eventsOutput

	// Apply yq expressions
	finalOutput, err := m.applyYQExpressions(combinedOutput, args)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(finalOutput), nil
}
