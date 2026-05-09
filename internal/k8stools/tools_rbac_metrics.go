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
	authv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// metricsServerError converts an API error coming from the metrics API into
// a user-friendly "metrics-server is not available" message when the failure
// is caused by the metrics.k8s.io API not being registered. Other errors are
// returned untouched.
func metricsServerError(err error) error {
	if err == nil {
		return nil
	}
	// metrics.k8s.io is served by an APIService backed by metrics-server. When
	// metrics-server is missing, the API server returns NotFound for the
	// "metrics" group resources.
	msg := err.Error()
	if apierrors.IsNotFound(err) || apierrors.IsServiceUnavailable(err) ||
		strings.Contains(msg, "metrics.k8s.io") || strings.Contains(msg, "could not find the requested resource") {
		return fmt.Errorf("metrics-server is not available in this cluster: %w", err)
	}
	return err
}

func (m *Manager) registerCheckPermission() {
	tool := mcp.NewTool(m.toolName("check_permission"),
		mcp.WithDescription(`Ask the API server whether the identity behind the kubeconfig is allowed
to perform a given verb on a given resource (Kubernetes
SelfSubjectAccessReview).

Verifies the cluster's native RBAC. Note that this is independent from the
MCP server's own authorization layer (which is checked first, before the
call ever reaches the cluster).

Useful as a pre-flight check before destructive operations, or to debug
"Forbidden" errors.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("verb", mcp.Required(), mcp.Description("RBAC verb to check. Standard values: 'get', 'list', 'watch', 'create', 'update', 'patch', 'delete', 'deletecollection'. Some resources accept extra verbs (e.g. 'use' on PodSecurityPolicies, 'bind' on ClusterRoles, 'impersonate' on Users).")),
		mcp.WithString("group", mcp.Description("API group of the resource being checked. Empty string \"\" for the core API.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Resource name in the API sense: lowercase plural ('pods', 'deployments', 'secrets'). NOT the Kind. Subresources can be checked as 'pods/exec', 'pods/log', 'deployments/scale'.")),
		mcp.WithString("name", mcp.Description("Optional resource instance name. When set, the check applies to that specific object; when empty, the check is for the resource type as a whole.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the check applies. Empty for cluster-scoped checks or for checks across all namespaces.")),
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

	// Split 'pods/exec' into ('pods', 'exec') so the SAR addresses the
	// subresource correctly (the API expects the two parts separately).
	subresource := ""
	if idx := strings.Index(resource, "/"); idx >= 0 {
		subresource = resource[idx+1:]
		resource = resource[:idx]
	}

	// Check authorization (real K8s resource: SelfSubjectAccessReview)
	if err := m.checkAuthorization(request, "check_permission", k8sContext, namespace, authorization.ResourceInfo{
		Group:    "authorization.k8s.io",
		Version:  "v1",
		Resource: "selfsubjectaccessreviews",
		Name:     name,
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
				Verb:        verb,
				Group:       group,
				Resource:    resource,
				Subresource: subresource,
				Name:        name,
				Namespace:   namespace,
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
	if result.Status.Denied {
		status = "denied (explicit)"
	}

	output := fmt.Sprintf("Permission check: %s\n", status)
	output += fmt.Sprintf("  Verb:        %s\n", verb)
	output += fmt.Sprintf("  Group:       %s\n", group)
	output += fmt.Sprintf("  Resource:    %s\n", resource)
	if subresource != "" {
		output += fmt.Sprintf("  Subresource: %s\n", subresource)
	}
	if name != "" {
		output += fmt.Sprintf("  Name:        %s\n", name)
	}
	if namespace != "" {
		output += fmt.Sprintf("  Namespace:   %s\n", namespace)
	}
	if result.Status.Reason != "" {
		output += fmt.Sprintf("  Reason:      %s\n", result.Status.Reason)
	}
	if result.Status.EvaluationError != "" {
		output += fmt.Sprintf("  Eval error:  %s\n", result.Status.EvaluationError)
	}

	return successResult(output), nil
}

func (m *Manager) registerGetPodMetrics() {
	tool := mcp.NewTool(m.toolName("get_pod_metrics"),
		mcp.WithDescription(`Return live CPU and memory usage for one Pod or many Pods.

Requires metrics-server to be installed in the cluster. If it is not, the
tool returns a clear "metrics-server is not available" error.

Selection rules:
  - 'name' set: returns metrics for that specific Pod (uses 'namespace',
    defaulting to 'default' if empty).
  - 'name' empty + 'namespace' set: lists metrics for all Pods in that
    namespace, optionally filtered by 'label_selector'.
  - both empty: lists Pod metrics across all namespaces (subject to RBAC).`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("namespace", mcp.Description("Namespace to scope the query to. See selection rules in the description.")),
		mcp.WithString("name", mcp.Description("Specific Pod name. If set, the response is a single PodMetrics object instead of a list.")),
		mcp.WithString("label_selector", mcp.Description("Kubernetes label selector. Only applied to the list flavours (when 'name' is empty).")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied to the YAML output. List flavour returns a PodMetricsList (use '.items[]'); single flavour returns a PodMetrics object. Examples: '.items[] | {pod: .metadata.name, cpu: .containers[0].usage.cpu}' (compact), '.items[].metadata.name' (just names).")),
	)
	m.mcpServer.AddTool(tool, m.handleGetPodMetrics)
}

func (m *Manager) handleGetPodMetrics(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	namespace, _ := args["namespace"].(string)
	name, _ := args["name"].(string)
	labelSelector, _ := args["label_selector"].(string)

	// Check authorization (real K8s resource: PodMetrics, surfaced under
	// metrics.k8s.io/v1beta1 with the standard 'pods' plural — same name
	// 'kubectl top pod' targets).
	if err := m.checkAuthorization(request, "get_pod_metrics", k8sContext, namespace, authorization.ResourceInfo{
		Group:    "metrics.k8s.io",
		Version:  "v1beta1",
		Resource: "pods",
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
		return errorResult(metricsServerError(err)), nil
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
	tool := mcp.NewTool(m.toolName("get_node_metrics"),
		mcp.WithDescription(`Return live CPU and memory usage for one Node or all Nodes.

Requires metrics-server to be installed in the cluster. If it is not, the
tool returns a clear "metrics-server is not available" error.

  - 'name' set: returns metrics for that specific Node.
  - 'name' empty: lists metrics for every Node, optionally filtered by
    'label_selector'.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("name", mcp.Description("Specific Node name. If set, the response is a single NodeMetrics object instead of a list.")),
		mcp.WithString("label_selector", mcp.Description("Kubernetes label selector. Only applied to the list flavour (when 'name' is empty). Examples: 'node-role.kubernetes.io/control-plane=', 'topology.kubernetes.io/zone=eu-west-1a'.")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied to the YAML output. List flavour returns a NodeMetricsList (use '.items[]'); single flavour returns a NodeMetrics object. Examples: '.items[] | {name: .metadata.name, cpu: .usage.cpu, memory: .usage.memory}' (compact), '.items[].metadata.name' (just names).")),
	)
	m.mcpServer.AddTool(tool, m.handleGetNodeMetrics)
}

func (m *Manager) handleGetNodeMetrics(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	labelSelector, _ := args["label_selector"].(string)

	// Check authorization (real K8s resource: NodeMetrics, surfaced under
	// metrics.k8s.io/v1beta1 with the standard 'nodes' plural — same name
	// 'kubectl top node' targets).
	if err := m.checkAuthorization(request, "get_node_metrics", k8sContext, "", authorization.ResourceInfo{
		Group:    "metrics.k8s.io",
		Version:  "v1beta1",
		Resource: "nodes",
		Name:     name,
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
		return errorResult(metricsServerError(err)), nil
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
