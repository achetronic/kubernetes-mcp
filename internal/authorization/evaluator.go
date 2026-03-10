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

	// Virtual resource kinds (used as resource names in GVR)
	VirtualResourceAPIDiscovery = "apidiscovery"
	VirtualResourceClusterInfo  = "clusterinfo"
	VirtualResourceContext      = "contexts"
)

// ToolVirtualResources maps tools to their virtual resources
var ToolVirtualResources = map[string]ResourceInfo{
	"list_api_resources":  {Group: VirtualResourceGroup, Resource: VirtualResourceAPIDiscovery},
	"list_api_versions":   {Group: VirtualResourceGroup, Resource: VirtualResourceAPIDiscovery},
	"get_cluster_info":    {Group: VirtualResourceGroup, Resource: VirtualResourceClusterInfo},
	"get_current_context": {Group: VirtualResourceGroup, Resource: VirtualResourceContext},
	"list_contexts":       {Group: VirtualResourceGroup, Resource: VirtualResourceContext},
	"switch_context":      {Group: VirtualResourceGroup, Resource: VirtualResourceContext},
}

// CompiledPolicy holds a policy with its precompiled CEL programs
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
	Payload   map[string]any
	Tool      string
	Context   string
	Namespace string
	Resource  ResourceInfo
}

// ResourceInfo holds information about the resource being accessed (GVR)
type ResourceInfo struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
	Name     string `json:"name"`
}

// NewEvaluator creates a new authorization evaluator
func NewEvaluator(config *api.AuthorizationConfig) (*Evaluator, error) {
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
	if resource.Resource == "" {
		if virtualRes, ok := ToolVirtualResources[tool]; ok {
			return virtualRes
		}
	}
	return resource
}

// Evaluate evaluates all matching policies and returns whether the request is allowed.
//
// Algorithm:
//  1. If no payload and anonymous not allowed -> deny
//  2. Find all policies whose CEL match expression is true
//  3. Collect all rules from matched policies
//  4. If ANY deny rule matches the request -> deny
//  5. If ANY allow rule matches the request -> allow
//  6. Default: deny
func (e *Evaluator) Evaluate(req AuthzRequest) (bool, error) {
	if len(req.Payload) == 0 && !e.config.AllowAnonymous {
		return false, nil
	}

	req.Resource = GetResourceForTool(req.Tool, req.Resource)

	evalCtx := map[string]any{
		"payload":   req.Payload,
		"tool":      req.Tool,
		"context":   req.Context,
		"namespace": req.Namespace,
		"resource": map[string]any{
			"group":    req.Resource.Group,
			"version":  req.Resource.Version,
			"resource": req.Resource.Resource,
			"name":     req.Resource.Name,
		},
	}

	var matchedRules []api.AuthorizationRule

	for _, cp := range e.compiledPolicies {
		out, _, err := cp.Program.Eval(evalCtx)
		if err != nil {
			continue
		}

		matched, ok := out.Value().(bool)
		if !ok || !matched {
			continue
		}

		matchedRules = append(matchedRules, cp.Policy.Rules...)
	}

	if len(matchedRules) == 0 {
		return false, nil
	}

	// Deny takes priority: if any deny rule matches, deny
	for _, rule := range matchedRules {
		if rule.Effect == api.RuleEffectDeny && ruleMatchesRequest(rule, req) {
			return false, nil
		}
	}

	// Check if any allow rule matches
	for _, rule := range matchedRules {
		if rule.Effect == api.RuleEffectAllow && ruleMatchesRequest(rule, req) {
			return true, nil
		}
	}

	return false, nil
}

// ruleMatchesRequest checks if a rule matches the given request
func ruleMatchesRequest(rule api.AuthorizationRule, req AuthzRequest) bool {
	if !matchesTool(rule.Tools, req.Tool) {
		return false
	}

	if !matchesContext(rule.Contexts, req.Context) {
		return false
	}

	if !matchesResources(rule.Resources, req.Resource, req.Namespace) {
		return false
	}

	return true
}

// matchesTool checks if a tool name matches any pattern in the list.
// Empty list matches everything. Supports glob patterns.
func matchesTool(patterns []string, tool string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if globMatch(pattern, tool) {
			return true
		}
	}
	return false
}

// matchesContext checks if a context matches any pattern in the list.
// Empty list matches everything. Supports glob patterns.
func matchesContext(patterns []string, ctx string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if globMatch(pattern, ctx) {
			return true
		}
	}
	return false
}

// matchesResources checks if a resource matches the resource rules.
// Empty resource list means the rule applies to all resources.
func matchesResources(rules []api.ResourceRule, resource ResourceInfo, namespace string) bool {
	if len(rules) == 0 {
		return true
	}
	for _, rule := range rules {
		if matchesSingleResourceRule(rule, resource, namespace) {
			return true
		}
	}
	return false
}

// matchesSingleResourceRule checks if a resource matches a single ResourceRule
func matchesSingleResourceRule(rule api.ResourceRule, resource ResourceInfo, namespace string) bool {
	if len(rule.Groups) > 0 && !matchesGlobList(rule.Groups, resource.Group) {
		return false
	}

	if len(rule.Versions) > 0 && !matchesGlobList(rule.Versions, resource.Version) {
		return false
	}

	if len(rule.Resources) > 0 && !matchesGlobList(rule.Resources, resource.Resource) {
		return false
	}

	if len(rule.Namespaces) > 0 && !matchesGlobList(rule.Namespaces, namespace) {
		return false
	}

	if len(rule.Names) > 0 && !matchesGlobList(rule.Names, resource.Name) {
		return false
	}

	return true
}

// matchesGlobList checks if a value matches any glob pattern in the list
func matchesGlobList(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if globMatch(pattern, value) {
			return true
		}
	}
	return false
}

// globMatch performs glob-style pattern matching.
// Supports:
//   - "*" matches everything
//   - "prefix*" matches strings starting with prefix
//   - "*suffix" matches strings ending with suffix
//   - "pre*suf" matches strings starting with pre and ending with suf
//   - "a*b*c" matches with multiple wildcards
//   - Exact match when no wildcards present
func globMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	return deepGlobMatch(pattern, value)
}

// deepGlobMatch handles complex glob patterns with multiple wildcards
// using a simple recursive approach with memoization-friendly structure
func deepGlobMatch(pattern, value string) bool {
	for len(pattern) > 0 {
		if pattern[0] == '*' {
			// Skip consecutive stars
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			// Trailing star matches everything
			if len(pattern) == 0 {
				return true
			}
			// Try matching the rest of the pattern at every position
			for i := 0; i <= len(value); i++ {
				if deepGlobMatch(pattern, value[i:]) {
					return true
				}
			}
			return false
		}

		if len(value) == 0 || pattern[0] != value[0] {
			return false
		}

		pattern = pattern[1:]
		value = value[1:]
	}

	return len(value) == 0
}
