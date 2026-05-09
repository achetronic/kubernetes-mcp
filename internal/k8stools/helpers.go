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
	"encoding/hex"
	"encoding/json"
	"fmt"

	"kubernetes-mcp/internal/authorization"
	"kubernetes-mcp/internal/kubernetes"
	"kubernetes-mcp/internal/middlewares"

	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// extractAuthPayload extracts the authentication payload from the request.
// It supports both JWT and API key authentication methods.
// The payload is read from the X-Auth-Payload header set by the auth middlewares.
func (m *Manager) extractAuthPayload(request mcp.CallToolRequest) map[string]any {
	payloadHex := request.Header.Get(middlewares.AuthPayloadHeader)
	if payloadHex == "" {
		return nil
	}

	payloadJSON, err := hex.DecodeString(payloadHex)
	if err != nil {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil
	}

	return payload
}

// checkAuthorization checks if the request is authorized
func (m *Manager) checkAuthorization(request mcp.CallToolRequest, tool, k8sContext, namespace string, resource authorization.ResourceInfo) error {
	if m.authz == nil {
		return nil
	}

	payload := m.extractAuthPayload(request)

	allowed, err := m.authz.Evaluate(authorization.AuthzRequest{
		Payload:   payload,
		Tool:      tool,
		Context:   k8sContext,
		Namespace: namespace,
		Resource:  resource,
	})
	if err != nil {
		return fmt.Errorf("authorization error: %w", err)
	}

	if !allowed {
		return fmt.Errorf("access denied: not authorized to use tool %s on context %s", tool, k8sContext)
	}

	return nil
}

// getContextParam extracts the context parameter or returns the current context
func (m *Manager) getContextParam(args map[string]any) string {
	if ctx, ok := args["context"].(string); ok && ctx != "" {
		return ctx
	}
	return m.clientManager.GetCurrentContext()
}

// applyYQExpressions applies yq expressions to the YAML output
func (m *Manager) applyYQExpressions(yamlData string, args map[string]any) (string, error) {
	exprs, ok := args["yq_expressions"].([]any)
	if !ok || len(exprs) == 0 {
		return yamlData, nil
	}

	var expressions []string
	for _, e := range exprs {
		if s, ok := e.(string); ok {
			expressions = append(expressions, s)
		}
	}

	return m.yq.Evaluate(yamlData, expressions)
}

// gvrFromArgs builds a GroupVersionResource directly from tool arguments.
// The model is expected to provide the resource as the lowercase plural
// (e.g. "pods", "deployments", "ingresses"). No Kind -> Resource heuristic.
func gvrFromArgs(args map[string]any) schema.GroupVersionResource {
	group, _ := args["group"].(string)
	version, _ := args["version"].(string)
	resource, _ := args["resource"].(string)
	return schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
}

// validateGVR returns a descriptive error if a GVR is missing required fields.
// Group is allowed to be empty (core API). Used by tool handlers to surface a
// clear error to the model when it forgot to pass `version` or `resource`, or
// (commonly) sent a Kind like "Pod" in the `resource` field.
func validateGVR(gvr schema.GroupVersionResource) error {
	if gvr.Version == "" {
		return fmt.Errorf("missing required parameter: version (e.g. \"v1\")")
	}
	if gvr.Resource == "" {
		return fmt.Errorf("missing required parameter: resource (lowercase plural, e.g. \"pods\", \"deployments\", \"ingresses\" — NOT the Kind)")
	}
	// Catch the most common mistake: sending a Kind in `resource`.
	first := gvr.Resource[0]
	if first >= 'A' && first <= 'Z' {
		return fmt.Errorf("parameter `resource` must be the lowercase plural form (e.g. \"pods\", \"deployments\"), not the Kind %q", gvr.Resource)
	}
	return nil
}

// resolveGVRForGVK resolves a GroupVersionKind to its real GroupVersionResource
// and namespaced flag using the cluster's discovery API via the RESTMapper.
// This is used by tools that receive a manifest (apply_manifest, diff_manifest)
// where the user provides Kind, not Resource.
func (m *Manager) resolveGVRForGVK(client *kubernetes.Client, gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool, error) {
	mapping, err := client.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, false, fmt.Errorf("failed to map %s to a resource via discovery: %w", gvk.String(), err)
	}
	return mapping.Resource, mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
}

// resolveKindForGVR resolves the Kind for a GroupVersionResource using the
// RESTMapper. Used when a tool needs the Kind (e.g. describe_resource filters
// related events by involvedObject.kind) but the user only provides a GVR.
func (m *Manager) resolveKindForGVR(client *kubernetes.Client, gvr schema.GroupVersionResource) (string, error) {
	gvk, err := client.RESTMapper.KindFor(gvr)
	if err != nil {
		return "", fmt.Errorf("failed to resolve kind for %s via discovery: %w", gvr.String(), err)
	}
	return gvk.Kind, nil
}

// objectToYAML converts an unstructured object to YAML
func objectToYAML(obj any) (string, error) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// errorResult creates an error result for MCP
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Error: %s", err.Error()),
			},
		},
		IsError: true,
	}
}

// successResult creates a success result for MCP
func successResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: text,
			},
		},
	}
}

// getListOptions builds list options from parameters
func getListOptions(args map[string]any) metav1.ListOptions {
	opts := metav1.ListOptions{}

	if ls, ok := args["label_selector"].(string); ok {
		opts.LabelSelector = ls
	}

	if fs, ok := args["field_selector"].(string); ok {
		opts.FieldSelector = fs
	}

	if lim, ok := args["limit"].(float64); ok && lim >= 1 {
		opts.Limit = int64(lim)
	}

	if c, ok := args["continue_token"].(string); ok && c != "" {
		opts.Continue = c
	}

	return opts
}

// getDeleteOptions builds delete options from parameters
func getDeleteOptions(args map[string]any) (metav1.DeleteOptions, error) {
	opts := metav1.DeleteOptions{}

	if gp, ok := args["grace_period_seconds"].(float64); ok {
		gpInt := int64(gp)
		opts.GracePeriodSeconds = &gpInt
	}

	if pp, ok := args["propagation_policy"].(string); ok && pp != "" {
		switch pp {
		case "Orphan", "Background", "Foreground":
			policy := metav1.DeletionPropagation(pp)
			opts.PropagationPolicy = &policy
		default:
			return opts, fmt.Errorf("invalid propagation_policy %q: expected one of Orphan, Background, Foreground", pp)
		}
	}

	return opts, nil
}
