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
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) registerCheckPermission() {
	tool := mcp.NewTool("check_permission",
		mcp.WithDescription("Checks if an action is allowed (SelfSubjectAccessReview)"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("verb", mcp.Required(), mcp.Description("Verb to check: get, list, create, update, delete, etc.")),
		mcp.WithString("group", mcp.Description("API group")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource type")),
		mcp.WithString("name", mcp.Description("Resource name (optional)")),
		mcp.WithString("namespace", mcp.Description("Namespace (optional)")),
	)
	m.mcpServer.AddTool(tool, m.handleCheckPermission)
}

func (m *Manager) handleCheckPermission(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	verb, _ := args["verb"].(string)
	group, _ := args["group"].(string)
	resource, _ := args["resource"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "check_permission", k8sContext, namespace, authorization.ResourceInfo{
		Group: group,
		Kind:  resource,
		Name:  name,
	}); err != nil {
		return errorResult(err), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	review := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Verb:      verb,
				Group:     group,
				Resource:  resource,
				Name:      name,
				Namespace: namespace,
			},
		},
	}

	result, err := client.Clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	status := "denied"
	if result.Status.Allowed {
		status = "allowed"
	}

	output := fmt.Sprintf("Permission check: %s\n", status)
	output += fmt.Sprintf("  Verb:      %s\n", verb)
	output += fmt.Sprintf("  Group:     %s\n", group)
	output += fmt.Sprintf("  Resource:  %s\n", resource)
	if name != "" {
		output += fmt.Sprintf("  Name:      %s\n", name)
	}
	if namespace != "" {
		output += fmt.Sprintf("  Namespace: %s\n", namespace)
	}
	if result.Status.Reason != "" {
		output += fmt.Sprintf("  Reason:    %s\n", result.Status.Reason)
	}

	return successResult(output), nil
}

func (m *Manager) registerGetPodMetrics() {
	tool := mcp.NewTool("get_pod_metrics",
		mcp.WithDescription("Gets CPU and memory usage for pods (requires metrics-server)"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("namespace", mcp.Description("Namespace (optional)")),
		mcp.WithString("name", mcp.Description("Pod name (optional, lists all if empty)")),
		mcp.WithString("label_selector", mcp.Description("Label selector")),
		mcp.WithArray("yq_expressions", mcp.Description(`Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.items[].metadata.name' (get pod names), '.items[].containers[].usage' (get resource usage), '.items[] | select(.containers[].usage.cpu > "100m")' (filter high CPU)`)),
	)
	m.mcpServer.AddTool(tool, m.handleGetPodMetrics)
}

func (m *Manager) handleGetPodMetrics(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	namespace, _ := args["namespace"].(string)
	name, _ := args["name"].(string)
	labelSelector, _ := args["label_selector"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "get_pod_metrics", k8sContext, namespace, authorization.ResourceInfo{
		Kind: "PodMetrics",
		Name: name,
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

	if client.MetricsClient == nil {
		return errorResult(fmt.Errorf("metrics-server is not available in this cluster")), nil
	}

	var result any
	if name != "" {
		// Get specific pod metrics
		if namespace == "" {
			namespace = "default"
		}
		result, err = client.MetricsClient.MetricsV1beta1().PodMetricses(namespace).Get(ctx, name, metav1.GetOptions{})
	} else if namespace != "" {
		// List pod metrics in namespace
		result, err = client.MetricsClient.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
	} else {
		// List all pod metrics
		result, err = client.MetricsClient.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
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

func (m *Manager) registerGetNodeMetrics() {
	tool := mcp.NewTool("get_node_metrics",
		mcp.WithDescription("Gets CPU and memory usage for nodes (requires metrics-server)"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("name", mcp.Description("Node name (optional, lists all if empty)")),
		mcp.WithString("label_selector", mcp.Description("Label selector")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.items[] | {name: .metadata.name, cpu: .usage.cpu, memory: .usage.memory}' (reshape output), '.items[].usage' (get all usage data)")),
	)
	m.mcpServer.AddTool(tool, m.handleGetNodeMetrics)
}

func (m *Manager) handleGetNodeMetrics(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	labelSelector, _ := args["label_selector"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "get_node_metrics", k8sContext, "", authorization.ResourceInfo{
		Kind: "NodeMetrics",
		Name: name,
	}); err != nil {
		return errorResult(err), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	if client.MetricsClient == nil {
		return errorResult(fmt.Errorf("metrics-server is not available in this cluster")), nil
	}

	var result any
	if name != "" {
		result, err = client.MetricsClient.MetricsV1beta1().NodeMetricses().Get(ctx, name, metav1.GetOptions{})
	} else {
		result, err = client.MetricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
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
