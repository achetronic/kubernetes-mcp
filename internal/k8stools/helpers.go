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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"kubernetes-mcp/internal/authorization"

	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// extractJWTPayload extracts the JWT payload from the request header
func (m *Manager) extractJWTPayload(request mcp.CallToolRequest) map[string]any {
	jwtHeader := m.config.Middleware.JWT.Validation.ForwardedHeader
	if jwtHeader == "" {
		return nil
	}

	tokenString := request.Header.Get(jwtHeader)
	if tokenString == "" {
		return nil
	}

	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil
	}

	return payload
}

// checkAuthorization checks if the request is authorized
func (m *Manager) checkAuthorization(request mcp.CallToolRequest, tool, k8sContext, namespace string, resource authorization.ResourceInfo) error {
	if m.authz == nil {
		return nil
	}

	payload := m.extractJWTPayload(request)

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

// getGVR builds a GroupVersionResource from parameters
func getGVR(group, version, kind string) schema.GroupVersionResource {
	// Convert kind to resource (lowercase plural)
	// This is a simplified conversion - in practice you might want to use discovery
	resource := strings.ToLower(kind)
	if !strings.HasSuffix(resource, "s") {
		resource += "s"
	}
	// Handle special cases
	switch strings.ToLower(kind) {
	case "ingress":
		resource = "ingresses"
	case "networkpolicy":
		resource = "networkpolicies"
	case "endpoints":
		resource = "endpoints"
	}

	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}
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

	return opts
}

// getDeleteOptions builds delete options from parameters
func getDeleteOptions(args map[string]any) metav1.DeleteOptions {
	opts := metav1.DeleteOptions{}

	if gp, ok := args["grace_period_seconds"].(float64); ok {
		gpInt := int64(gp)
		opts.GracePeriodSeconds = &gpInt
	}

	if pp, ok := args["propagation_policy"].(string); ok {
		policy := metav1.DeletionPropagation(pp)
		opts.PropagationPolicy = &policy
	}

	return opts
}
