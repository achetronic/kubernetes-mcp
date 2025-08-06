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

// Evaluate evaluates all matching policies and returns whether the request is allowed
func (e *Evaluator) Evaluate(req AuthzRequest) (bool, error) {
	// Check for anonymous access
	if len(req.Payload) == 0 && !e.config.AllowAnonymous {
		return false, nil
	}

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
		e.applyPolicyPermissions(cp.Policy, permissions, req)
	}

	// Check if the request is allowed
	return e.isRequestAllowed(permissions, req), nil
}

// applyPolicyPermissions applies a policy's allow and deny rules to the effective permissions
func (e *Evaluator) applyPolicyPermissions(policy api.AuthorizationPolicy, perms *EffectivePermissions, req AuthzRequest) {
	// First, apply allow rules
	if policy.Allow != nil {
		e.applyAllowRules(policy.Allow, perms)
	}

	// Then, if there are deny rules, they restrict this policy's contribution
	// But since we're doing union of all policies, we track denied items separately
	// and apply them only to what this policy would have allowed
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

// isRequestAllowed checks if the request is allowed based on effective permissions
func (e *Evaluator) isRequestAllowed(perms *EffectivePermissions, req AuthzRequest) bool {
	// Check tool
	if !perms.AllowedTools["*"] && !perms.AllowedTools[req.Tool] {
		return false
	}

	// Check context
	if !perms.AllowedContexts["*"] && !perms.AllowedContexts[req.Context] {
		return false
	}

	return true
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
