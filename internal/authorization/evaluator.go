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

package authorization

import (
	"fmt"
	"strings"

	"kubernetes-mcp/api"

	"github.com/google/cel-go/cel"
)

const (
	// VirtualResourceGroup is the API group for MCP virtual resources
	VirtualResourceGroup = "_"

	// Virtual resource kinds
	VirtualKindAPIDiscovery = "APIDiscovery"
	VirtualKindClusterInfo  = "ClusterInfo"
	VirtualKindContext      = "Context"
)

// ToolVirtualResources maps tools to their virtual resources
var ToolVirtualResources = map[string]ResourceInfo{
	"list_api_resources":  {Group: VirtualResourceGroup, Kind: VirtualKindAPIDiscovery},
	"list_api_versions":   {Group: VirtualResourceGroup, Kind: VirtualKindAPIDiscovery},
	"get_cluster_info":    {Group: VirtualResourceGroup, Kind: VirtualKindClusterInfo},
	"get_current_context": {Group: VirtualResourceGroup, Kind: VirtualKindContext},
	"list_contexts":       {Group: VirtualResourceGroup, Kind: VirtualKindContext},
	"switch_context":      {Group: VirtualResourceGroup, Kind: VirtualKindContext},
}

// CompiledPolicy holds a policy with its precompiled CEL program
type CompiledPolicy struct {
	Policy  api.AuthorizationPolicy
	Program cel.Program
}

// Evaluator evaluates authorization policies using CEL
type Evaluator struct {
	config           *api.AuthorizationConfig
	compiledPolicies []CompiledPolicy
	celEnv           *cel.Env
}

// AuthzRequest represents the data available for authorization evaluation
type AuthzRequest struct {
	Payload   map[string]any // JWT claims
	Tool      string         // Tool being invoked
	Context   string         // Kubernetes context
	Namespace string         // Resource namespace (if applicable)
	Resource  ResourceInfo   // Resource information
}

// ResourceInfo holds information about the resource being accessed
type ResourceInfo struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}

// EffectivePermissions represents the computed permissions for a request
type EffectivePermissions struct {
	AllowedTools              map[string]bool
	AllowedContexts           map[string]bool
	AllowedResources          []api.ResourceRule
	DeniedResources           []api.ResourceRule
	AllowedLabelPrefixes      map[string]bool
	AllowedAnnotationPrefixes map[string]bool
}

// NewEvaluator creates a new authorization evaluator
func NewEvaluator(config *api.AuthorizationConfig) (*Evaluator, error) {
	// Create CEL environment with available variables
	env, err := cel.NewEnv(
		cel.Variable("payload", cel.DynType),
		cel.Variable("tool", cel.StringType),
		cel.Variable("context", cel.StringType),
		cel.Variable("namespace", cel.StringType),
		cel.Variable("resource", cel.DynType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	e := &Evaluator{
		config:           config,
		celEnv:           env,
		compiledPolicies: make([]CompiledPolicy, 0, len(config.Policies)),
	}

	// Precompile all policies
	for _, policy := range config.Policies {
		ast, issues := env.Compile(policy.Match.Expression)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("failed to compile policy %s: %w", policy.Name, issues.Err())
		}

		prg, err := env.Program(ast)
		if err != nil {
			return nil, fmt.Errorf("failed to create program for policy %s: %w", policy.Name, err)
		}

		e.compiledPolicies = append(e.compiledPolicies, CompiledPolicy{
			Policy:  policy,
			Program: prg,
		})
	}

	return e, nil
}

// GetResourceForTool returns the ResourceInfo for a tool, applying virtual resource mapping if needed
func GetResourceForTool(tool string, resource ResourceInfo) ResourceInfo {
	// If the tool has a virtual resource mapping and no real resource is provided, use the virtual one
	if resource.Kind == "" {
		if virtualRes, ok := ToolVirtualResources[tool]; ok {
			return virtualRes
		}
	}
	return resource
}

// Evaluate evaluates all matching policies and returns whether the request is allowed
func (e *Evaluator) Evaluate(req AuthzRequest) (bool, error) {
	// Check for anonymous access
	if len(req.Payload) == 0 && !e.config.AllowAnonymous {
		return false, nil
	}

	// Apply virtual resource mapping if needed
	req.Resource = GetResourceForTool(req.Tool, req.Resource)

	// Build CEL evaluation context
	evalCtx := map[string]any{
		"payload":   req.Payload,
		"tool":      req.Tool,
		"context":   req.Context,
		"namespace": req.Namespace,
		"resource": map[string]any{
			"group":   req.Resource.Group,
			"version": req.Resource.Version,
			"kind":    req.Resource.Kind,
			"name":    req.Resource.Name,
		},
	}

	// Find all matching policies and compute effective permissions
	permissions := &EffectivePermissions{
		AllowedTools:              make(map[string]bool),
		AllowedContexts:           make(map[string]bool),
		AllowedResources:          make([]api.ResourceRule, 0),
		DeniedResources:           make([]api.ResourceRule, 0),
		AllowedLabelPrefixes:      make(map[string]bool),
		AllowedAnnotationPrefixes: make(map[string]bool),
	}

	for _, cp := range e.compiledPolicies {
		// Evaluate match expression
		out, _, err := cp.Program.Eval(evalCtx)
		if err != nil {
			// Expression evaluation error - skip this policy
			continue
		}

		matched, ok := out.Value().(bool)
		if !ok || !matched {
			continue
		}

		// Policy matched - compute effective permissions (allow - deny)
		e.applyPolicyPermissions(cp.Policy, permissions)
	}

	// Check if the request is allowed
	return e.isRequestAllowed(permissions, req), nil
}

// applyPolicyPermissions applies a policy's allow and deny rules to the effective permissions
func (e *Evaluator) applyPolicyPermissions(policy api.AuthorizationPolicy, perms *EffectivePermissions) {
	// Apply allow rules
	if policy.Allow != nil {
		e.applyAllowRules(policy.Allow, perms)
	}

	// Apply deny rules
	if policy.Deny != nil {
		e.applyDenyRules(policy.Deny, perms)
	}
}

// applyAllowRules applies allow rules to permissions
func (e *Evaluator) applyAllowRules(rule *api.ToolContextRule, perms *EffectivePermissions) {
	for _, tool := range rule.Tools {
		if tool == "*" {
			perms.AllowedTools["*"] = true
		} else {
			perms.AllowedTools[tool] = true
		}
	}

	for _, ctx := range rule.Contexts {
		if ctx == "*" {
			perms.AllowedContexts["*"] = true
		} else {
			perms.AllowedContexts[ctx] = true
		}
	}

	// Collect allowed resources
	perms.AllowedResources = append(perms.AllowedResources, rule.Resources...)

	for _, prefix := range rule.LabelPrefixes {
		if prefix == "*" {
			perms.AllowedLabelPrefixes["*"] = true
		} else {
			perms.AllowedLabelPrefixes[prefix] = true
		}
	}

	for _, prefix := range rule.AnnotationPrefixes {
		if prefix == "*" {
			perms.AllowedAnnotationPrefixes["*"] = true
		} else {
			perms.AllowedAnnotationPrefixes[prefix] = true
		}
	}
}

// applyDenyRules applies deny rules to permissions
func (e *Evaluator) applyDenyRules(rule *api.ToolContextRule, perms *EffectivePermissions) {
	// Collect denied resources
	perms.DeniedResources = append(perms.DeniedResources, rule.Resources...)

	// For tools and contexts, remove from allowed if explicitly denied
	for _, tool := range rule.Tools {
		if tool == "*" {
			// Deny all - clear allowed tools except keep track of wildcard deny
			perms.AllowedTools = make(map[string]bool)
			perms.AllowedTools["_denied_*"] = true
		} else {
			delete(perms.AllowedTools, tool)
		}
	}

	for _, ctx := range rule.Contexts {
		if ctx == "*" {
			perms.AllowedContexts = make(map[string]bool)
			perms.AllowedContexts["_denied_*"] = true
		} else {
			delete(perms.AllowedContexts, ctx)
		}
	}
}

// isRequestAllowed checks if the request is allowed based on effective permissions
func (e *Evaluator) isRequestAllowed(perms *EffectivePermissions, req AuthzRequest) bool {
	// Check for wildcard deny
	if perms.AllowedTools["_denied_*"] || perms.AllowedContexts["_denied_*"] {
		return false
	}

	// Check tool
	if !perms.AllowedTools["*"] && !perms.AllowedTools[req.Tool] {
		return false
	}

	// Check context
	if !perms.AllowedContexts["*"] && !perms.AllowedContexts[req.Context] {
		return false
	}

	// Check resources
	if !e.isResourceAllowed(perms, req.Resource, req.Namespace) {
		return false
	}

	return true
}

// isResourceAllowed checks if the resource is allowed based on allow/deny rules
func (e *Evaluator) isResourceAllowed(perms *EffectivePermissions, resource ResourceInfo, namespace string) bool {
	// If no resource rules are defined, allow all resources
	if len(perms.AllowedResources) == 0 && len(perms.DeniedResources) == 0 {
		return true
	}

	// First check if explicitly denied (deny wins over allow)
	if len(perms.DeniedResources) > 0 && matchesResourceRules(perms.DeniedResources, resource, namespace) {
		return false
	}

	// If no allow rules, but there are deny rules and we passed them, allow
	if len(perms.AllowedResources) == 0 {
		return true
	}

	// Check if allowed by at least one rule
	return matchesResourceRules(perms.AllowedResources, resource, namespace)
}

// matchesResourceRules checks if a resource matches any of the rules
func matchesResourceRules(rules []api.ResourceRule, resource ResourceInfo, namespace string) bool {
	for _, rule := range rules {
		if matchesResourceRule(rule, resource, namespace) {
			return true
		}
	}
	return false
}

// matchesResourceRule checks if a resource matches a single rule
func matchesResourceRule(rule api.ResourceRule, resource ResourceInfo, namespace string) bool {
	// Check groups
	if len(rule.Groups) > 0 && !matchesList(rule.Groups, resource.Group) {
		return false
	}

	// Check versions
	if len(rule.Versions) > 0 && !matchesList(rule.Versions, resource.Version) {
		return false
	}

	// Check kinds
	if len(rule.Kinds) > 0 && !matchesList(rule.Kinds, resource.Kind) {
		return false
	}

	// Check namespaces (with wildcard support)
	if len(rule.Namespaces) > 0 && !matchesWildcardList(rule.Namespaces, namespace) {
		return false
	}

	// Check names (with wildcard support)
	if len(rule.Names) > 0 && !matchesWildcardList(rule.Names, resource.Name) {
		return false
	}

	return true
}

// matchesList checks if value matches any item in list (supports "*" wildcard)
func matchesList(list []string, value string) bool {
	for _, item := range list {
		if item == "*" || item == value {
			return true
		}
	}
	return false
}

// matchesWildcardList checks if value matches any pattern in the list
func matchesWildcardList(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matchesWildcard(pattern, value) {
			return true
		}
	}
	return false
}

// matchesWildcard checks if a value matches a wildcard pattern
// Supports: "*" (all), "prefix-*", "*-suffix", "*-middle-*"
func matchesWildcard(pattern, value string) bool {
	// Exact match or wildcard all
	if pattern == "*" || pattern == value {
		return true
	}

	// Handle patterns with wildcards
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")

		// Single wildcard cases
		if len(parts) == 2 {
			prefix := parts[0]
			suffix := parts[1]

			// "prefix-*" pattern
			if suffix == "" {
				return strings.HasPrefix(value, prefix)
			}

			// "*-suffix" pattern
			if prefix == "" {
				return strings.HasSuffix(value, suffix)
			}

			// "prefix-*-suffix" pattern
			return strings.HasPrefix(value, prefix) && strings.HasSuffix(value, suffix) && len(value) >= len(prefix)+len(suffix)
		}

		// Multiple wildcards: "*-middle-*" pattern
		if len(parts) == 3 && parts[0] == "" && parts[2] == "" {
			return strings.Contains(value, parts[1])
		}
	}

	return false
}

// IsLabelPrefixAllowed checks if a label prefix is allowed
func (e *Evaluator) IsLabelPrefixAllowed(req AuthzRequest, labelKey string) (bool, error) {
	// Build CEL evaluation context
	evalCtx := map[string]any{
		"payload":   req.Payload,
		"tool":      req.Tool,
		"context":   req.Context,
		"namespace": req.Namespace,
		"resource": map[string]any{
			"group":   req.Resource.Group,
			"version": req.Resource.Version,
			"kind":    req.Resource.Kind,
			"name":    req.Resource.Name,
		},
	}

	allowedPrefixes := make(map[string]bool)
	deniedPrefixes := make(map[string]bool)

	for _, cp := range e.compiledPolicies {
		out, _, err := cp.Program.Eval(evalCtx)
		if err != nil {
			continue
		}

		matched, ok := out.Value().(bool)
		if !ok || !matched {
			continue
		}

		// Collect allowed prefixes
		if cp.Policy.Allow != nil {
			for _, prefix := range cp.Policy.Allow.LabelPrefixes {
				allowedPrefixes[prefix] = true
			}
		}

		// Collect denied prefixes (only affects this policy's contribution)
		if cp.Policy.Deny != nil {
			for _, prefix := range cp.Policy.Deny.LabelPrefixes {
				deniedPrefixes[prefix] = true
			}
		}
	}

	// Wildcard allows everything
	if allowedPrefixes["*"] {
		// Check if specifically denied
		for prefix := range deniedPrefixes {
			if strings.HasPrefix(labelKey, prefix) {
				// Check if another policy allows it
				for allowedPrefix := range allowedPrefixes {
					if allowedPrefix != "*" && strings.HasPrefix(labelKey, allowedPrefix) {
						return true, nil
					}
				}
				return false, nil
			}
		}
		return true, nil
	}

	// Check if any allowed prefix matches
	for prefix := range allowedPrefixes {
		if strings.HasPrefix(labelKey, prefix) {
			return true, nil
		}
	}

	return false, nil
}

// IsAnnotationPrefixAllowed checks if an annotation prefix is allowed
func (e *Evaluator) IsAnnotationPrefixAllowed(req AuthzRequest, annotationKey string) (bool, error) {
	// Same logic as labels
	evalCtx := map[string]any{
		"payload":   req.Payload,
		"tool":      req.Tool,
		"context":   req.Context,
		"namespace": req.Namespace,
		"resource": map[string]any{
			"group":   req.Resource.Group,
			"version": req.Resource.Version,
			"kind":    req.Resource.Kind,
			"name":    req.Resource.Name,
		},
	}

	allowedPrefixes := make(map[string]bool)
	deniedPrefixes := make(map[string]bool)

	for _, cp := range e.compiledPolicies {
		out, _, err := cp.Program.Eval(evalCtx)
		if err != nil {
			continue
		}

		matched, ok := out.Value().(bool)
		if !ok || !matched {
			continue
		}

		if cp.Policy.Allow != nil {
			for _, prefix := range cp.Policy.Allow.AnnotationPrefixes {
				allowedPrefixes[prefix] = true
			}
		}

		if cp.Policy.Deny != nil {
			for _, prefix := range cp.Policy.Deny.AnnotationPrefixes {
				deniedPrefixes[prefix] = true
			}
		}
	}

	if allowedPrefixes["*"] {
		for prefix := range deniedPrefixes {
			if strings.HasPrefix(annotationKey, prefix) {
				for allowedPrefix := range allowedPrefixes {
					if allowedPrefix != "*" && strings.HasPrefix(annotationKey, allowedPrefix) {
						return true, nil
					}
				}
				return false, nil
			}
		}
		return true, nil
	}

	for prefix := range allowedPrefixes {
		if strings.HasPrefix(annotationKey, prefix) {
			return true, nil
		}
	}

	return false, nil
}

// GetIdentity extracts the identity from the JWT payload based on the configured claim
func (e *Evaluator) GetIdentity(payload map[string]any) string {
	if e.config.IdentityClaim == "" {
		return ""
	}

	if val, ok := payload[e.config.IdentityClaim]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}

	return ""
}
