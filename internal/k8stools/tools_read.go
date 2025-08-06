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
	tool := mcp.NewTool("get_resource",
		mcp.WithDescription("Gets a specific Kubernetes resource by name"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use (optional, uses current if not specified)")),
		mcp.WithString("group", mcp.Description("API group (e.g., 'apps', 'batch', empty for core)")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version (e.g., 'v1', 'v1beta1')")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind (e.g., 'Pod', 'Deployment')")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("namespace", mcp.Description("Namespace (optional for cluster-scoped resources)")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.metadata.name' (get name), '.spec.containers[].image' (get all container images), 'select(.status.phase == \"Running\")' (filter by condition), '.items[] | {name: .metadata.name, status: .status.phase}' (reshape output)")),
	)
	m.mcpServer.AddTool(tool, m.handleGetResource)
}

func (m *Manager) handleGetResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	group, _ := args["group"].(string)
	version, _ := args["version"].(string)
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "get_resource", k8sContext, namespace, authorization.ResourceInfo{
		Group:   group,
		Version: version,
		Kind:    kind,
		Name:    name,
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

	gvr := getGVR(group, version, kind)

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
	tool := mcp.NewTool("list_resources",
		mcp.WithDescription("Lists Kubernetes resources with optional filters"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("namespace", mcp.Description("Namespace (empty for all namespaces)")),
		mcp.WithString("label_selector", mcp.Description("Label selector (e.g., 'app=nginx,env!=prod')")),
		mcp.WithString("field_selector", mcp.Description("Field selector (e.g., 'metadata.name=foo')")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.metadata.name' (get name), '.spec.containers[].image' (get all container images), 'select(.status.phase == \"Running\")' (filter by condition), '.items[] | {name: .metadata.name, status: .status.phase}' (reshape output)")),
	)
	m.mcpServer.AddTool(tool, m.handleListResources)
}

func (m *Manager) handleListResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	group, _ := args["group"].(string)
	version, _ := args["version"].(string)
	kind, _ := args["kind"].(string)
	namespace, _ := args["namespace"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "list_resources", k8sContext, namespace, authorization.ResourceInfo{
		Group:   group,
		Version: version,
		Kind:    kind,
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

	gvr := getGVR(group, version, kind)
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
	tool := mcp.NewTool("describe_resource",
		mcp.WithDescription("Gets detailed information about a resource including related events"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.metadata.name' (get name), '.spec.containers[].image' (get all container images), 'select(.status.phase == \"Running\")' (filter by condition), '.items[] | {name: .metadata.name, status: .status.phase}' (reshape output)")),
	)
	m.mcpServer.AddTool(tool, m.handleDescribeResource)
}

func (m *Manager) handleDescribeResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	group, _ := args["group"].(string)
	version, _ := args["version"].(string)
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "describe_resource", k8sContext, namespace, authorization.ResourceInfo{
		Group:   group,
		Version: version,
		Kind:    kind,
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

	gvr := getGVR(group, version, kind)

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

	// Get related events
	eventsOutput := ""
	if namespace != "" {
		events, err := client.Clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
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
