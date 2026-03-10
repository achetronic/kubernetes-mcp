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
	"testing"

	"kubernetes-mcp/api"
)

// ============================================================================
// Glob matching tests
// ============================================================================

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		// Exact match
		{"foo", "foo", true},
		{"foo", "bar", false},
		{"", "", true},
		{"foo", "", false},
		{"", "foo", false},

		// Single wildcard = match all
		{"*", "", true},
		{"*", "anything", true},
		{"*", "foo/bar", true},

		// Prefix wildcard
		{"get_*", "get_resource", true},
		{"get_*", "get_logs", true},
		{"get_*", "get_", true},
		{"get_*", "list_resources", false},
		{"get_*", "get", false},
		{"prefix*", "prefix", true},
		{"prefix*", "prefixsuffix", true},
		{"prefix*", "pre", false},

		// Suffix wildcard
		{"*_resource", "get_resource", true},
		{"*_resource", "delete_resource", true},
		{"*_resource", "resource", false},
		{"*_resource", "get_resources", false},
		{"*.go", "main.go", true},
		{"*.go", "test.go", true},
		{"*.go", "go", false},

		// Prefix + suffix wildcard
		{"get_*_status", "get_rollout_status", true},
		{"get_*_status", "get_status", false},
		{"get_*_status", "get__status", true},
		{"a*c", "abc", true},
		{"a*c", "ac", true},
		{"a*c", "abdc", true},
		{"a*c", "abd", false},

		// Multiple wildcards
		{"*-*", "foo-bar", true},
		{"*-*", "-", true},
		{"*-*", "foobar", false},
		{"a*b*c", "abc", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"*team-*", "my-team-ns", true},
		{"*team-*", "team-", true},

		// Kubernetes-style patterns
		{"apps", "apps", true},
		{"apps", "batch", false},
		{"v1*", "v1", true},
		{"v1*", "v1beta1", true},
		{"v1*", "v2", false},
		{"deploy*", "deployments", true},
		{"deploy*", "deploy", true},
		{"*maps", "configmaps", true},
		{"*maps", "maps", true},
		{"*maps", "map", false},

		// Edge cases
		{"**", "anything", true},
		{"**", "", true},
		{"a**b", "ab", true},
		{"a**b", "aXb", true},
		{"*a*", "bac", true},
		{"*a*", "bcd", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q matches %q", tt.pattern, tt.value), func(t *testing.T) {
			got := globMatch(tt.pattern, tt.value)
			if got != tt.want {
				t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
			}
		})
	}
}

// ============================================================================
// Anonymous access tests
// ============================================================================

func TestAnonymousAccess(t *testing.T) {
	tests := []struct {
		name           string
		allowAnonymous bool
		payload        map[string]any
		want           bool
	}{
		{
			name:           "anonymous denied when disabled",
			allowAnonymous: false,
			payload:        nil,
			want:           false,
		},
		{
			name:           "empty payload denied when disabled",
			allowAnonymous: false,
			payload:        map[string]any{},
			want:           false,
		},
		{
			name:           "anonymous allowed when enabled with matching policy",
			allowAnonymous: true,
			payload:        nil,
			want:           true,
		},
		{
			name:           "empty payload allowed when enabled with matching policy",
			allowAnonymous: true,
			payload:        map[string]any{},
			want:           true,
		},
		{
			name:           "authenticated user allowed when anonymous disabled",
			allowAnonymous: false,
			payload:        map[string]any{"sub": "user@example.com"},
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &api.AuthorizationConfig{
				AllowAnonymous: tt.allowAnonymous,
				Policies: []api.AuthorizationPolicy{
					{
						Name:  "allow-all",
						Match: api.MatchConfig{Expression: "true"},
						Rules: []api.AuthorizationRule{
							{
								Effect:   api.RuleEffectAllow,
								Tools:    []string{"*"},
								Contexts: []string{"*"},
							},
						},
					},
				},
			}

			eval, err := NewEvaluator(config)
			if err != nil {
				t.Fatalf("NewEvaluator: %v", err)
			}

			allowed, err := eval.Evaluate(AuthzRequest{
				Payload: tt.payload,
				Tool:    "get_resource",
				Context: "prod",
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != tt.want {
				t.Errorf("got %v, want %v", allowed, tt.want)
			}
		})
	}
}

// ============================================================================
// CEL match expression tests
// ============================================================================

func TestCELMatchExpressions(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		payload    map[string]any
		want       bool
	}{
		{
			name:       "always true",
			expression: "true",
			payload:    map[string]any{"sub": "user"},
			want:       true,
		},
		{
			name:       "always false",
			expression: "false",
			payload:    map[string]any{"sub": "user"},
			want:       false,
		},
		{
			name:       "email match",
			expression: `payload.email == "admin@company.com"`,
			payload:    map[string]any{"email": "admin@company.com"},
			want:       true,
		},
		{
			name:       "email no match",
			expression: `payload.email == "admin@company.com"`,
			payload:    map[string]any{"email": "user@company.com"},
			want:       false,
		},
		{
			name:       "group membership with exists",
			expression: `payload.groups.exists(g, g == "sre-team")`,
			payload:    map[string]any{"groups": []any{"sre-team", "devs"}},
			want:       true,
		},
		{
			name:       "group membership no match",
			expression: `payload.groups.exists(g, g == "sre-team")`,
			payload:    map[string]any{"groups": []any{"devs", "qa"}},
			want:       false,
		},
		{
			name:       "email domain with endsWith",
			expression: `payload.email.endsWith("@company.com")`,
			payload:    map[string]any{"email": "admin@company.com"},
			want:       true,
		},
		{
			name:       "has operator check",
			expression: `has(payload.admin) && payload.admin == true`,
			payload:    map[string]any{"admin": true},
			want:       true,
		},
		{
			name:       "has operator missing field",
			expression: `has(payload.admin) && payload.admin == true`,
			payload:    map[string]any{"sub": "user"},
			want:       false,
		},
		{
			name:       "combined conditions",
			expression: `payload.groups.exists(g, g == "devs") && payload.email.endsWith("@company.com")`,
			payload:    map[string]any{"groups": []any{"devs"}, "email": "user@company.com"},
			want:       true,
		},
		{
			name:       "in operator for groups",
			expression: `"sre" in payload.groups`,
			payload:    map[string]any{"groups": []any{"sre", "devs"}},
			want:       true,
		},
		{
			name:       "anonymous check",
			expression: `!has(payload.sub)`,
			payload:    map[string]any{},
			want:       true,
		},
		{
			name:       "client_id match",
			expression: `payload.azp == "ci-cd-client" || payload.client_id == "ci-cd-client"`,
			payload:    map[string]any{"client_id": "ci-cd-client"},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &api.AuthorizationConfig{
				AllowAnonymous: true,
				Policies: []api.AuthorizationPolicy{
					{
						Name:  "test",
						Match: api.MatchConfig{Expression: tt.expression},
						Rules: []api.AuthorizationRule{
							{
								Effect:   api.RuleEffectAllow,
								Tools:    []string{"*"},
								Contexts: []string{"*"},
							},
						},
					},
				},
			}

			eval, err := NewEvaluator(config)
			if err != nil {
				t.Fatalf("NewEvaluator: %v", err)
			}

			allowed, err := eval.Evaluate(AuthzRequest{
				Payload: tt.payload,
				Tool:    "get_resource",
				Context: "prod",
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != tt.want {
				t.Errorf("got %v, want %v", allowed, tt.want)
			}
		})
	}
}

// ============================================================================
// Tool glob pattern matching tests
// ============================================================================

func TestToolGlobMatching(t *testing.T) {
	tests := []struct {
		name         string
		allowedTools []string
		tool         string
		want         bool
	}{
		{"wildcard allows all", []string{"*"}, "anything", true},
		{"exact match", []string{"get_resource"}, "get_resource", true},
		{"exact no match", []string{"get_resource"}, "list_resources", false},
		{"prefix glob", []string{"get_*"}, "get_resource", true},
		{"prefix glob no match", []string{"get_*"}, "list_resources", false},
		{"suffix glob", []string{"*_resource"}, "get_resource", true},
		{"suffix glob no match", []string{"*_resources"}, "get_resource", false},
		{"multiple patterns", []string{"get_*", "list_*"}, "list_resources", true},
		{"multiple patterns no match", []string{"get_*", "list_*"}, "delete_resource", false},
		{"complex glob", []string{"*_rollout*"}, "restart_rollout", true},
		{"complex glob 2", []string{"*_rollout*"}, "get_rollout_status", true},
		{"empty tools list matches all", []string{}, "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    tt.allowedTools,
					Contexts: []string{"*"},
				},
			}

			config := &api.AuthorizationConfig{
				AllowAnonymous: true,
				Policies: []api.AuthorizationPolicy{
					{
						Name:  "test",
						Match: api.MatchConfig{Expression: "true"},
						Rules: rules,
					},
				},
			}

			eval, err := NewEvaluator(config)
			if err != nil {
				t.Fatalf("NewEvaluator: %v", err)
			}

			allowed, err := eval.Evaluate(AuthzRequest{
				Payload: map[string]any{},
				Tool:    tt.tool,
				Context: "prod",
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != tt.want {
				t.Errorf("got %v, want %v", allowed, tt.want)
			}
		})
	}
}

// ============================================================================
// Context matching tests
// ============================================================================

func TestContextMatching(t *testing.T) {
	tests := []struct {
		name     string
		contexts []string
		ctx      string
		want     bool
	}{
		{"wildcard", []string{"*"}, "production", true},
		{"exact match", []string{"production"}, "production", true},
		{"exact no match", []string{"production"}, "staging", false},
		{"multiple contexts", []string{"production", "staging"}, "staging", true},
		{"glob prefix", []string{"prod-*"}, "prod-us-east", true},
		{"glob prefix no match", []string{"prod-*"}, "staging-us", false},
		{"glob suffix", []string{"*-east"}, "prod-us-east", true},
		{"empty list matches all", []string{}, "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &api.AuthorizationConfig{
				AllowAnonymous: true,
				Policies: []api.AuthorizationPolicy{
					{
						Name:  "test",
						Match: api.MatchConfig{Expression: "true"},
						Rules: []api.AuthorizationRule{
							{
								Effect:   api.RuleEffectAllow,
								Tools:    []string{"*"},
								Contexts: tt.contexts,
							},
						},
					},
				},
			}

			eval, err := NewEvaluator(config)
			if err != nil {
				t.Fatalf("NewEvaluator: %v", err)
			}

			allowed, err := eval.Evaluate(AuthzRequest{
				Payload: map[string]any{},
				Tool:    "get_resource",
				Context: tt.ctx,
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != tt.want {
				t.Errorf("got %v, want %v", allowed, tt.want)
			}
		})
	}
}

// ============================================================================
// Resource GVR matching tests
// ============================================================================

func TestResourceGVRMatching(t *testing.T) {
	tests := []struct {
		name      string
		rule      api.ResourceRule
		resource  ResourceInfo
		namespace string
		want      bool
	}{
		// Group matching
		{
			name:     "core group match with empty string",
			rule:     api.ResourceRule{Groups: []string{""}},
			resource: ResourceInfo{Group: "", Resource: "pods"},
			want:     true,
		},
		{
			name:     "core group no match",
			rule:     api.ResourceRule{Groups: []string{""}},
			resource: ResourceInfo{Group: "apps", Resource: "deployments"},
			want:     false,
		},
		{
			name:     "apps group match",
			rule:     api.ResourceRule{Groups: []string{"apps"}},
			resource: ResourceInfo{Group: "apps", Resource: "deployments"},
			want:     true,
		},
		{
			name:     "wildcard group",
			rule:     api.ResourceRule{Groups: []string{"*"}},
			resource: ResourceInfo{Group: "anything", Resource: "pods"},
			want:     true,
		},
		{
			name:     "multiple groups",
			rule:     api.ResourceRule{Groups: []string{"", "apps", "batch"}},
			resource: ResourceInfo{Group: "batch", Resource: "jobs"},
			want:     true,
		},
		{
			name:     "glob group pattern",
			rule:     api.ResourceRule{Groups: []string{"*.k8s.io"}},
			resource: ResourceInfo{Group: "networking.k8s.io", Resource: "ingresses"},
			want:     true,
		},
		{
			name:     "virtual group",
			rule:     api.ResourceRule{Groups: []string{"_"}},
			resource: ResourceInfo{Group: "_", Resource: "contexts"},
			want:     true,
		},

		// Version matching
		{
			name:     "v1 version match",
			rule:     api.ResourceRule{Versions: []string{"v1"}},
			resource: ResourceInfo{Version: "v1", Resource: "pods"},
			want:     true,
		},
		{
			name:     "v1 version no match",
			rule:     api.ResourceRule{Versions: []string{"v1"}},
			resource: ResourceInfo{Version: "v1beta1", Resource: "pods"},
			want:     false,
		},
		{
			name:     "version glob",
			rule:     api.ResourceRule{Versions: []string{"v1*"}},
			resource: ResourceInfo{Version: "v1beta1", Resource: "pods"},
			want:     true,
		},
		{
			name:     "omitted version matches anything",
			rule:     api.ResourceRule{},
			resource: ResourceInfo{Version: "v1beta2", Resource: "pods"},
			want:     true,
		},

		// Resource (plural) matching
		{
			name:     "exact resource match",
			rule:     api.ResourceRule{Resources: []string{"pods"}},
			resource: ResourceInfo{Resource: "pods"},
			want:     true,
		},
		{
			name:     "resource no match",
			rule:     api.ResourceRule{Resources: []string{"pods"}},
			resource: ResourceInfo{Resource: "deployments"},
			want:     false,
		},
		{
			name:     "wildcard resource",
			rule:     api.ResourceRule{Resources: []string{"*"}},
			resource: ResourceInfo{Resource: "anything"},
			want:     true,
		},
		{
			name:     "resource glob",
			rule:     api.ResourceRule{Resources: []string{"deploy*"}},
			resource: ResourceInfo{Resource: "deployments"},
			want:     true,
		},
		{
			name:     "multiple resources",
			rule:     api.ResourceRule{Resources: []string{"pods", "services", "configmaps"}},
			resource: ResourceInfo{Resource: "services"},
			want:     true,
		},

		// Namespace matching
		{
			name:      "exact namespace",
			rule:      api.ResourceRule{Namespaces: []string{"default"}},
			resource:  ResourceInfo{Resource: "pods"},
			namespace: "default",
			want:      true,
		},
		{
			name:      "namespace no match",
			rule:      api.ResourceRule{Namespaces: []string{"default"}},
			resource:  ResourceInfo{Resource: "pods"},
			namespace: "production",
			want:      false,
		},
		{
			name:      "namespace glob prefix",
			rule:      api.ResourceRule{Namespaces: []string{"team-*"}},
			resource:  ResourceInfo{Resource: "pods"},
			namespace: "team-backend",
			want:      true,
		},
		{
			name:      "namespace glob no match",
			rule:      api.ResourceRule{Namespaces: []string{"team-*"}},
			resource:  ResourceInfo{Resource: "pods"},
			namespace: "production",
			want:      false,
		},
		{
			name:      "cluster-scoped with empty string",
			rule:      api.ResourceRule{Namespaces: []string{""}},
			resource:  ResourceInfo{Resource: "nodes"},
			namespace: "",
			want:      true,
		},
		{
			name:      "cluster-scoped no match namespaced",
			rule:      api.ResourceRule{Namespaces: []string{""}},
			resource:  ResourceInfo{Resource: "pods"},
			namespace: "default",
			want:      false,
		},
		{
			name:      "omitted namespace matches any",
			rule:      api.ResourceRule{},
			resource:  ResourceInfo{Resource: "pods"},
			namespace: "anything",
			want:      true,
		},
		{
			name:      "omitted namespace matches cluster-scoped",
			rule:      api.ResourceRule{},
			resource:  ResourceInfo{Resource: "nodes"},
			namespace: "",
			want:      true,
		},

		// Name matching
		{
			name:     "exact name match",
			rule:     api.ResourceRule{Names: []string{"my-pod"}},
			resource: ResourceInfo{Resource: "pods", Name: "my-pod"},
			want:     true,
		},
		{
			name:     "name no match",
			rule:     api.ResourceRule{Names: []string{"my-pod"}},
			resource: ResourceInfo{Resource: "pods", Name: "other-pod"},
			want:     false,
		},
		{
			name:     "name glob",
			rule:     api.ResourceRule{Names: []string{"my-app-*"}},
			resource: ResourceInfo{Resource: "pods", Name: "my-app-abc123"},
			want:     true,
		},
		{
			name:     "omitted names matches any",
			rule:     api.ResourceRule{},
			resource: ResourceInfo{Resource: "pods", Name: "anything"},
			want:     true,
		},

		// Combined filters
		{
			name: "full GVR + namespace + name match",
			rule: api.ResourceRule{
				Groups:     []string{"apps"},
				Versions:   []string{"v1"},
				Resources:  []string{"deployments"},
				Namespaces: []string{"production"},
				Names:      []string{"api-server"},
			},
			resource:  ResourceInfo{Group: "apps", Version: "v1", Resource: "deployments", Name: "api-server"},
			namespace: "production",
			want:      true,
		},
		{
			name: "full GVR match but wrong namespace",
			rule: api.ResourceRule{
				Groups:     []string{"apps"},
				Versions:   []string{"v1"},
				Resources:  []string{"deployments"},
				Namespaces: []string{"production"},
			},
			resource:  ResourceInfo{Group: "apps", Version: "v1", Resource: "deployments"},
			namespace: "staging",
			want:      false,
		},
		{
			name: "full GVR match but wrong name",
			rule: api.ResourceRule{
				Groups:    []string{"apps"},
				Resources: []string{"deployments"},
				Names:     []string{"api-*"},
			},
			resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "worker-service"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesSingleResourceRule(tt.rule, tt.resource, tt.namespace)
			if got != tt.want {
				t.Errorf("matchesSingleResourceRule() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================================
// Deny takes priority tests
// ============================================================================

func TestDenyTakesPriority(t *testing.T) {
	tests := []struct {
		name    string
		rules   []api.AuthorizationRule
		req     AuthzRequest
		want    bool
	}{
		{
			name: "allow all then deny specific tool",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
				{
					Effect:   api.RuleEffectDeny,
					Tools:    []string{"delete_resource"},
					Contexts: []string{"*"},
				},
			},
			req:  AuthzRequest{Payload: map[string]any{}, Tool: "delete_resource", Context: "prod"},
			want: false,
		},
		{
			name: "allow all then deny specific - non-denied tool passes",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
				{
					Effect:   api.RuleEffectDeny,
					Tools:    []string{"delete_resource"},
					Contexts: []string{"*"},
				},
			},
			req:  AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "prod"},
			want: true,
		},
		{
			name: "deny all tools",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
				{
					Effect:   api.RuleEffectDeny,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
			},
			req:  AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "prod"},
			want: false,
		},
		{
			name: "deny specific context",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
				{
					Effect:   api.RuleEffectDeny,
					Tools:    []string{"*"},
					Contexts: []string{"production"},
				},
			},
			req:  AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "production"},
			want: false,
		},
		{
			name: "deny production but allow staging",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
				{
					Effect:   api.RuleEffectDeny,
					Tools:    []string{"*"},
					Contexts: []string{"production"},
				},
			},
			req:  AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "staging"},
			want: true,
		},
		{
			name: "deny specific resource",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
					Resources: []api.ResourceRule{
						{Groups: []string{"*"}, Resources: []string{"*"}},
					},
				},
				{
					Effect: api.RuleEffectDeny,
					Resources: []api.ResourceRule{
						{Groups: []string{""}, Resources: []string{"secrets"}},
					},
				},
			},
			req: AuthzRequest{
				Payload:   map[string]any{},
				Tool:      "get_resource",
				Context:   "prod",
				Namespace: "default",
				Resource:  ResourceInfo{Group: "", Resource: "secrets", Name: "my-secret"},
			},
			want: false,
		},
		{
			name: "deny secrets but allow configmaps",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
					Resources: []api.ResourceRule{
						{Groups: []string{"*"}, Resources: []string{"*"}},
					},
				},
				{
					Effect: api.RuleEffectDeny,
					Resources: []api.ResourceRule{
						{Groups: []string{""}, Resources: []string{"secrets"}},
					},
				},
			},
			req: AuthzRequest{
				Payload:   map[string]any{},
				Tool:      "get_resource",
				Context:   "prod",
				Namespace: "default",
				Resource:  ResourceInfo{Group: "", Resource: "configmaps", Name: "my-config"},
			},
			want: true,
		},
		{
			name: "deny with tool glob pattern",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
				{
					Effect:   api.RuleEffectDeny,
					Tools:    []string{"delete_*"},
					Contexts: []string{"production"},
				},
			},
			req:  AuthzRequest{Payload: map[string]any{}, Tool: "delete_resource", Context: "production"},
			want: false,
		},
		{
			name: "deny with tool glob - non-matching tool passes",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				},
				{
					Effect:   api.RuleEffectDeny,
					Tools:    []string{"delete_*"},
					Contexts: []string{"production"},
				},
			},
			req:  AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "production"},
			want: true,
		},
		{
			name: "deny namespace glob",
			rules: []api.AuthorizationRule{
				{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
					Resources: []api.ResourceRule{
						{Groups: []string{"*"}, Resources: []string{"*"}},
					},
				},
				{
					Effect: api.RuleEffectDeny,
					Resources: []api.ResourceRule{
						{Groups: []string{"*"}, Resources: []string{"*"}, Namespaces: []string{"kube-*"}},
					},
				},
			},
			req: AuthzRequest{
				Payload:   map[string]any{},
				Tool:      "get_resource",
				Context:   "prod",
				Namespace: "kube-system",
				Resource:  ResourceInfo{Group: "", Resource: "pods"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &api.AuthorizationConfig{
				AllowAnonymous: true,
				Policies: []api.AuthorizationPolicy{
					{
						Name:  "test",
						Match: api.MatchConfig{Expression: "true"},
						Rules: tt.rules,
					},
				},
			}

			eval, err := NewEvaluator(config)
			if err != nil {
				t.Fatalf("NewEvaluator: %v", err)
			}

			allowed, err := eval.Evaluate(tt.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != tt.want {
				t.Errorf("got %v, want %v", allowed, tt.want)
			}
		})
	}
}

// ============================================================================
// Multi-policy interaction tests
// ============================================================================

func TestMultiPolicyInteraction(t *testing.T) {
	t.Run("multiple policies - deny from any policy wins", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "allow-all",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
					},
				},
				{
					Name:  "deny-delete-in-prod",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectDeny,
							Tools:    []string{"delete_*"},
							Contexts: []string{"production"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		// delete in prod should be denied
		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "delete_resource",
			Context: "production",
		})
		if allowed {
			t.Error("delete in prod should be denied")
		}

		// get in prod should be allowed
		allowed, _ = eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "get_resource",
			Context: "production",
		})
		if !allowed {
			t.Error("get in prod should be allowed")
		}

		// delete in staging should be allowed
		allowed, _ = eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "delete_resource",
			Context: "staging",
		})
		if !allowed {
			t.Error("delete in staging should be allowed")
		}
	})

	t.Run("no matching policy denies", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: false,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "sre-only",
					Match: api.MatchConfig{Expression: `"sre" in payload.groups`},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		// Non-SRE user should be denied
		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{"sub": "user", "groups": []any{"devs"}},
			Tool:    "get_resource",
			Context: "prod",
		})
		if allowed {
			t.Error("non-SRE user should be denied")
		}

		// SRE user should be allowed
		allowed, _ = eval.Evaluate(AuthzRequest{
			Payload: map[string]any{"sub": "admin", "groups": []any{"sre"}},
			Tool:    "get_resource",
			Context: "prod",
		})
		if !allowed {
			t.Error("SRE user should be allowed")
		}
	})

	t.Run("policies with different user matches", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: false,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "sre-full-access",
					Match: api.MatchConfig{Expression: `"sre" in payload.groups`},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
							Resources: []api.ResourceRule{
								{Groups: []string{"*"}, Resources: []string{"*"}},
								{Groups: []string{"_"}, Resources: []string{"*"}},
							},
						},
					},
				},
				{
					Name:  "devs-read-only",
					Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"get_*", "list_*", "describe_*"},
							Contexts: []string{"*"},
						},
						{
							Effect:   api.RuleEffectDeny,
							Tools:    []string{"delete_*", "apply_*", "patch_*"},
							Contexts: []string{"*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		// Dev user can read
		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{"sub": "dev", "groups": []any{"devs"}},
			Tool:    "get_resource",
			Context: "prod",
		})
		if !allowed {
			t.Error("dev should be able to read")
		}

		// Dev user can't delete
		allowed, _ = eval.Evaluate(AuthzRequest{
			Payload: map[string]any{"sub": "dev", "groups": []any{"devs"}},
			Tool:    "delete_resource",
			Context: "prod",
		})
		if allowed {
			t.Error("dev should not be able to delete")
		}

		// SRE user can delete
		allowed, _ = eval.Evaluate(AuthzRequest{
			Payload: map[string]any{"sub": "sre", "groups": []any{"sre"}},
			Tool:    "delete_resource",
			Context: "prod",
		})
		if !allowed {
			t.Error("SRE should be able to delete")
		}
	})
}

// ============================================================================
// Virtual resource tests
// ============================================================================

func TestVirtualResources(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		resource ResourceInfo
		wantGrp  string
		wantRes  string
	}{
		{
			name:    "list_api_resources maps to virtual",
			tool:    "list_api_resources",
			wantGrp: VirtualResourceGroup,
			wantRes: VirtualResourceAPIDiscovery,
		},
		{
			name:    "list_api_versions maps to virtual",
			tool:    "list_api_versions",
			wantGrp: VirtualResourceGroup,
			wantRes: VirtualResourceAPIDiscovery,
		},
		{
			name:    "get_cluster_info maps to virtual",
			tool:    "get_cluster_info",
			wantGrp: VirtualResourceGroup,
			wantRes: VirtualResourceClusterInfo,
		},
		{
			name:    "get_current_context maps to virtual",
			tool:    "get_current_context",
			wantGrp: VirtualResourceGroup,
			wantRes: VirtualResourceContext,
		},
		{
			name:    "list_contexts maps to virtual",
			tool:    "list_contexts",
			wantGrp: VirtualResourceGroup,
			wantRes: VirtualResourceContext,
		},
		{
			name:    "switch_context maps to virtual",
			tool:    "switch_context",
			wantGrp: VirtualResourceGroup,
			wantRes: VirtualResourceContext,
		},
		{
			name:     "real resource not overridden",
			tool:     "get_resource",
			resource: ResourceInfo{Group: "apps", Resource: "deployments"},
			wantGrp:  "apps",
			wantRes:  "deployments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetResourceForTool(tt.tool, tt.resource)
			if got.Group != tt.wantGrp {
				t.Errorf("Group = %q, want %q", got.Group, tt.wantGrp)
			}
			if got.Resource != tt.wantRes {
				t.Errorf("Resource = %q, want %q", got.Resource, tt.wantRes)
			}
		})
	}
}

func TestVirtualResourceAuthorization(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: true,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "allow-k8s-deny-virtual",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"*"}, Resources: []string{"*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{"_"}, Resources: []string{"*"}},
						},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	// Real K8s resource should be allowed
	allowed, _ := eval.Evaluate(AuthzRequest{
		Payload:   map[string]any{},
		Tool:      "get_resource",
		Context:   "prod",
		Namespace: "default",
		Resource:  ResourceInfo{Group: "apps", Resource: "deployments"},
	})
	if !allowed {
		t.Error("real K8s resource should be allowed")
	}

	// Virtual resource should be denied
	allowed, _ = eval.Evaluate(AuthzRequest{
		Payload: map[string]any{},
		Tool:    "list_api_resources",
		Context: "prod",
	})
	if allowed {
		t.Error("virtual resource should be denied")
	}
}

// ============================================================================
// Full scenario: SRE team production RBAC
// ============================================================================

func TestScenarioSREProduction(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "sre-full-access",
				Match: api.MatchConfig{Expression: `"sre" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"*"}, Resources: []string{"*"}},
							{Groups: []string{"_"}, Resources: []string{"*"}},
						},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	srePayload := map[string]any{"sub": "sre-user", "groups": []any{"sre"}}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"get pods", AuthzRequest{Payload: srePayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Version: "v1", Resource: "pods", Name: "my-pod"}}, true},
		{"delete secrets", AuthzRequest{Payload: srePayload, Tool: "delete_resource", Context: "production", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Version: "v1", Resource: "secrets", Name: "tls-cert"}}, true},
		{"exec in pods", AuthzRequest{Payload: srePayload, Tool: "exec_command", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Version: "v1", Resource: "pods", Name: "my-pod"}}, true},
		{"list contexts", AuthzRequest{Payload: srePayload, Tool: "list_contexts", Context: ""}, true},
		{"cluster info", AuthzRequest{Payload: srePayload, Tool: "get_cluster_info", Context: "production"}, true},
		{"scale deployments", AuthzRequest{Payload: srePayload, Tool: "scale_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Version: "v1", Resource: "deployments", Name: "api"}}, true},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Full scenario: Developers with restricted access
// ============================================================================

func TestScenarioDevelopers(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "devs-read",
				Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"get_*", "list_*", "describe_*", "get_logs"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"", "apps", "batch", "networking.k8s.io"}, Resources: []string{"*"}},
							{Groups: []string{"_"}, Resources: []string{"*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets"}},
							{Groups: []string{"rbac.authorization.k8s.io"}, Resources: []string{"*"}},
						},
					},
				},
			},
			{
				Name:  "devs-write-dev-staging",
				Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"apply_manifest", "patch_resource", "delete_resource", "scale_resource", "restart_rollout"},
						Contexts: []string{"development", "staging"},
						Resources: []api.ResourceRule{
							{Groups: []string{"", "apps", "batch", "networking.k8s.io"}, Resources: []string{"*"}, Namespaces: []string{"team-*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets"}},
						},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	devPayload := map[string]any{"sub": "dev-user", "groups": []any{"devs"}}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		// Read operations
		{"read pods in prod", AuthzRequest{Payload: devPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "my-pod"}}, true},
		{"list deployments in prod", AuthzRequest{Payload: devPayload, Tool: "list_resources", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}, true},
		{"get logs in prod", AuthzRequest{Payload: devPayload, Tool: "get_logs", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "my-pod"}}, true},
		{"read secrets denied", AuthzRequest{Payload: devPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "my-secret"}}, false},
		{"read RBAC denied", AuthzRequest{Payload: devPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Name: "admin"}}, false},
		{"list contexts allowed", AuthzRequest{Payload: devPayload, Tool: "list_contexts", Context: ""}, true},

		// Write operations in dev/staging
		{"apply in dev team ns", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "development", Namespace: "team-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"apply in staging team ns", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "staging", Namespace: "team-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"apply in prod denied", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "production", Namespace: "team-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"apply in non-team ns denied", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "development", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"apply secrets denied even in dev", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "development", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "my-secret"}}, false},

		// Tools not allowed
		{"exec denied", AuthzRequest{Payload: devPayload, Tool: "exec_command", Context: "development", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "my-pod"}}, false},
		{"delete_resources denied (bulk)", AuthzRequest{Payload: devPayload, Tool: "delete_resources", Context: "development", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Full scenario: CI/CD service account
// ============================================================================

func TestScenarioCICD(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "cicd-deploy",
				Match: api.MatchConfig{Expression: `payload.client_id == "ci-cd-service"`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"apply_manifest", "get_resource", "list_resources", "diff_manifest", "get_rollout_status"},
						Contexts: []string{"staging", "production"},
						Resources: []api.ResourceRule{
							{
								Groups:     []string{"", "apps", "networking.k8s.io"},
								Resources:  []string{"deployments", "services", "configmaps", "ingresses"},
								Namespaces: []string{"app-*"},
							},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Tools:  []string{"delete_*", "exec_*"},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets"}},
						},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	cicdPayload := map[string]any{"client_id": "ci-cd-service"}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"apply deployment in app-backend", AuthzRequest{Payload: cicdPayload, Tool: "apply_manifest", Context: "production", Namespace: "app-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api-server"}}, true},
		{"get rollout status", AuthzRequest{Payload: cicdPayload, Tool: "get_rollout_status", Context: "production", Namespace: "app-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api-server"}}, true},
		{"diff manifest", AuthzRequest{Payload: cicdPayload, Tool: "diff_manifest", Context: "staging", Namespace: "app-frontend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "web"}}, true},
		{"apply in non-app namespace denied", AuthzRequest{Payload: cicdPayload, Tool: "apply_manifest", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"apply in dev denied", AuthzRequest{Payload: cicdPayload, Tool: "apply_manifest", Context: "development", Namespace: "app-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"delete denied", AuthzRequest{Payload: cicdPayload, Tool: "delete_resource", Context: "staging", Namespace: "app-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"exec denied", AuthzRequest{Payload: cicdPayload, Tool: "exec_command", Context: "staging", Namespace: "app-backend", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "api-pod"}}, false},
		{"secrets denied", AuthzRequest{Payload: cicdPayload, Tool: "get_resource", Context: "production", Namespace: "app-backend", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "tls"}}, false},
		{"pods not in allowed resources", AuthzRequest{Payload: cicdPayload, Tool: "get_resource", Context: "production", Namespace: "app-backend", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "my-pod"}}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Full scenario: On-call with time-based access
// ============================================================================

func TestScenarioOnCall(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "oncall-operations",
				Match: api.MatchConfig{Expression: `"oncall" in payload.groups && payload.oncall_active == true`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"get_*", "list_*", "describe_*", "get_logs", "restart_rollout", "scale_resource", "get_rollout_status"},
						Contexts: []string{"production"},
						Resources: []api.ResourceRule{
							{Groups: []string{"", "apps"}, Resources: []string{"*"}},
							{Groups: []string{"metrics.k8s.io"}, Resources: []string{"*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Tools:  []string{"delete_*", "exec_*"},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	activeOncall := map[string]any{"sub": "user", "groups": []any{"oncall"}, "oncall_active": true}
	inactiveOncall := map[string]any{"sub": "user", "groups": []any{"oncall"}, "oncall_active": false}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"active: get pods", AuthzRequest{Payload: activeOncall, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"active: restart rollout", AuthzRequest{Payload: activeOncall, Tool: "restart_rollout", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"active: scale", AuthzRequest{Payload: activeOncall, Tool: "scale_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"active: get logs", AuthzRequest{Payload: activeOncall, Tool: "get_logs", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "api-pod"}}, true},
		{"active: delete denied", AuthzRequest{Payload: activeOncall, Tool: "delete_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"active: exec denied", AuthzRequest{Payload: activeOncall, Tool: "exec_command", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "api-pod"}}, false},
		{"active: staging denied", AuthzRequest{Payload: activeOncall, Tool: "get_resource", Context: "staging", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
		{"inactive: everything denied", AuthzRequest{Payload: inactiveOncall, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Full scenario: Multi-role user (devs + oncall)
// ============================================================================

func TestScenarioMultiRoleUser(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "devs-read",
				Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"get_*", "list_*"},
						Contexts: []string{"*"},
					},
				},
			},
			{
				Name:  "oncall-prod-ops",
				Match: api.MatchConfig{Expression: `"oncall" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"restart_rollout", "scale_resource"},
						Contexts: []string{"production"},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	multiRolePayload := map[string]any{"sub": "user", "groups": []any{"devs", "oncall"}}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"read from devs policy", AuthzRequest{Payload: multiRolePayload, Tool: "get_resource", Context: "production"}, true},
		{"list from devs policy", AuthzRequest{Payload: multiRolePayload, Tool: "list_resources", Context: "staging"}, true},
		{"restart from oncall policy", AuthzRequest{Payload: multiRolePayload, Tool: "restart_rollout", Context: "production"}, true},
		{"scale from oncall policy", AuthzRequest{Payload: multiRolePayload, Tool: "scale_resource", Context: "production"}, true},
		{"restart in staging denied (oncall only allows prod)", AuthzRequest{Payload: multiRolePayload, Tool: "restart_rollout", Context: "staging"}, false},
		{"delete denied (no policy allows)", AuthzRequest{Payload: multiRolePayload, Tool: "delete_resource", Context: "production"}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Full scenario: Namespace isolation
// ============================================================================

func TestScenarioNamespaceIsolation(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "team-backend",
				Match: api.MatchConfig{Expression: `payload.team == "backend"`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{
								Groups:     []string{"*"},
								Resources:  []string{"*"},
								Namespaces: []string{"backend-*", "shared-*"},
							},
							{Groups: []string{"_"}, Resources: []string{"*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets"}},
							{Groups: []string{"*"}, Resources: []string{"*"}, Namespaces: []string{"kube-*"}},
						},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	backendPayload := map[string]any{"sub": "dev", "team": "backend"}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"own namespace", AuthzRequest{Payload: backendPayload, Tool: "get_resource", Context: "prod", Namespace: "backend-api", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}, true},
		{"shared namespace", AuthzRequest{Payload: backendPayload, Tool: "get_resource", Context: "prod", Namespace: "shared-infra", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}, true},
		{"frontend namespace denied", AuthzRequest{Payload: backendPayload, Tool: "get_resource", Context: "prod", Namespace: "frontend-web", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}, false},
		{"kube-system denied", AuthzRequest{Payload: backendPayload, Tool: "get_resource", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
		{"secrets denied even in own ns", AuthzRequest{Payload: backendPayload, Tool: "get_resource", Context: "prod", Namespace: "backend-api", Resource: ResourceInfo{Group: "", Resource: "secrets"}}, false},
		{"virtual resources allowed", AuthzRequest{Payload: backendPayload, Tool: "list_contexts", Context: ""}, true},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Full scenario: Resource name filtering
// ============================================================================

func TestScenarioResourceNameFiltering(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "app-team",
				Match: api.MatchConfig{Expression: `"app-team" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{
								Groups:    []string{"apps"},
								Resources: []string{"deployments", "statefulsets"},
								Names:     []string{"myapp-*", "frontend-*"},
							},
							{
								Groups:    []string{""},
								Resources: []string{"services", "configmaps"},
								Names:     []string{"myapp-*", "frontend-*"},
							},
						},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	appTeamPayload := map[string]any{"sub": "dev", "groups": []any{"app-team"}}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"own deployment", AuthzRequest{Payload: appTeamPayload, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "myapp-api"}}, true},
		{"own frontend", AuthzRequest{Payload: appTeamPayload, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "frontend-web"}}, true},
		{"other team deployment", AuthzRequest{Payload: appTeamPayload, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "other-api"}}, false},
		{"own service", AuthzRequest{Payload: appTeamPayload, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "services", Name: "myapp-svc"}}, true},
		{"other service", AuthzRequest{Payload: appTeamPayload, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "services", Name: "other-svc"}}, false},
		{"pods not in allowed resources", AuthzRequest{Payload: appTeamPayload, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "myapp-pod"}}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Empty rules / edge cases
// ============================================================================

func TestEdgeCases(t *testing.T) {
	t.Run("no policies denies everything", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies:       []api.AuthorizationPolicy{},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "get_resource",
			Context: "prod",
		})
		if allowed {
			t.Error("no policies should deny")
		}
	})

	t.Run("policy with no rules denies", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "empty",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "get_resource",
			Context: "prod",
		})
		if allowed {
			t.Error("empty rules should deny")
		}
	})

	t.Run("deny-only policy denies", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "deny-only",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectDeny,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "get_resource",
			Context: "prod",
		})
		if allowed {
			t.Error("deny-only should deny")
		}
	})

	t.Run("allow with empty tools matches all tools", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "test",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Contexts: []string{"*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "anything",
			Context: "prod",
		})
		if !allowed {
			t.Error("empty tools should match all")
		}
	})

	t.Run("allow with empty contexts matches all contexts", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "test",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect: api.RuleEffectAllow,
							Tools:  []string{"*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "get_resource",
			Context: "any-context",
		})
		if !allowed {
			t.Error("empty contexts should match all")
		}
	})

	t.Run("allow with empty resources matches all resources", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "test",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload:   map[string]any{},
			Tool:      "get_resource",
			Context:   "prod",
			Namespace: "default",
			Resource:  ResourceInfo{Group: "apps", Resource: "deployments", Name: "my-deploy"},
		})
		if !allowed {
			t.Error("empty resources should match all")
		}
	})

	t.Run("CEL error in match is skipped", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "bad-cel",
					Match: api.MatchConfig{Expression: `payload.nonexistent.deeply.nested == "foo"`},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
					},
				},
				{
					Name:  "good-policy",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"get_*"},
							Contexts: []string{"*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		allowed, err := eval.Evaluate(AuthzRequest{
			Payload: map[string]any{},
			Tool:    "get_resource",
			Context: "prod",
		})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if !allowed {
			t.Error("good policy should still allow after bad CEL is skipped")
		}
	})

	t.Run("invalid CEL expression fails at compile time", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "invalid",
					Match: api.MatchConfig{Expression: "this is not valid CEL !!!"},
					Rules: []api.AuthorizationRule{},
				},
			},
		}

		_, err := NewEvaluator(config)
		if err == nil {
			t.Error("should fail on invalid CEL")
		}
	})
}

// ============================================================================
// Full scenario: Complex enterprise config
// ============================================================================

func TestScenarioEnterpriseConfig(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			// SRE: full access everywhere
			{
				Name:  "sre-full",
				Match: api.MatchConfig{Expression: `"sre" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"*"}, Resources: []string{"*"}},
							{Groups: []string{"_"}, Resources: []string{"*"}},
						},
					},
				},
			},
			// Developers: read in all contexts, write in dev/staging
			{
				Name:  "devs-read",
				Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"get_*", "list_*", "describe_*", "get_logs", "get_rollout_status"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"", "apps", "batch", "networking.k8s.io"}, Resources: []string{"*"}},
							{Groups: []string{"metrics.k8s.io"}, Resources: []string{"*"}},
							{Groups: []string{"_"}, Resources: []string{"*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets"}},
							{Groups: []string{"rbac.authorization.k8s.io"}, Resources: []string{"*"}},
						},
					},
				},
			},
			{
				Name:  "devs-write",
				Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"apply_manifest", "patch_resource", "delete_resource", "scale_resource", "restart_rollout"},
						Contexts: []string{"development", "staging"},
						Resources: []api.ResourceRule{
							{Groups: []string{"", "apps", "networking.k8s.io"}, Resources: []string{"*"}, Namespaces: []string{"team-*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets"}},
						},
					},
				},
			},
			// Global: no exec in production
			{
				Name:  "no-exec-prod",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectDeny,
						Tools:    []string{"exec_command"},
						Contexts: []string{"production"},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	srePayload := map[string]any{"sub": "sre-admin", "groups": []any{"sre"}}
	devPayload := map[string]any{"sub": "developer", "groups": []any{"devs"}}
	multiPayload := map[string]any{"sub": "senior", "groups": []any{"sre", "devs"}}
	noGroupPayload := map[string]any{"sub": "visitor"}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		// SRE scenarios
		{"sre: get pods prod", AuthzRequest{Payload: srePayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"sre: delete secrets prod", AuthzRequest{Payload: srePayload, Tool: "delete_resource", Context: "production", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "secrets"}}, true},
		{"sre: exec in prod DENIED by global rule", AuthzRequest{Payload: srePayload, Tool: "exec_command", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
		{"sre: exec in staging allowed", AuthzRequest{Payload: srePayload, Tool: "exec_command", Context: "staging", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"sre: cluster info", AuthzRequest{Payload: srePayload, Tool: "get_cluster_info", Context: "production"}, true},

		// Dev scenarios
		{"dev: get pods prod", AuthzRequest{Payload: devPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"dev: get deployments prod", AuthzRequest{Payload: devPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}, true},
		{"dev: get secrets DENIED", AuthzRequest{Payload: devPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets"}}, false},
		{"dev: get rbac DENIED", AuthzRequest{Payload: devPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}}, false},
		{"dev: apply in dev team-ns", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "development", Namespace: "team-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"dev: apply in prod DENIED", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "production", Namespace: "team-backend", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"dev: apply in default ns DENIED", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "development", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, false},
		{"dev: apply secrets DENIED even in team-ns", AuthzRequest{Payload: devPayload, Tool: "apply_manifest", Context: "development", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "my-secret"}}, false},
		{"dev: get metrics", AuthzRequest{Payload: devPayload, Tool: "get_pod_metrics", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "metrics.k8s.io", Resource: "podmetrics"}}, true},
		{"dev: exec in prod DENIED", AuthzRequest{Payload: devPayload, Tool: "exec_command", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
		{"dev: exec in dev DENIED (tool not in allow list)", AuthzRequest{Payload: devPayload, Tool: "exec_command", Context: "development", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},

		// Multi-role (SRE+Dev)
		{"multi: exec in prod still DENIED (global rule)", AuthzRequest{Payload: multiPayload, Tool: "exec_command", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
		{"multi: delete in prod (from SRE)", AuthzRequest{Payload: multiPayload, Tool: "delete_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}, true},

		// No group user
		{"no-group: denied", AuthzRequest{Payload: noGroupPayload, Tool: "get_resource", Context: "production", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Deny rule scope: deny with empty fields
// ============================================================================

func TestDenyRuleScope(t *testing.T) {
	t.Run("deny with empty tools matches all tools", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "test",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
						{
							Effect:   api.RuleEffectDeny,
							Contexts: []string{"production"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		// Any tool in production should be denied
		allowed, _ := eval.Evaluate(AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "production"})
		if allowed {
			t.Error("should deny any tool in production")
		}

		// Staging should still work
		allowed, _ = eval.Evaluate(AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "staging"})
		if !allowed {
			t.Error("staging should be allowed")
		}
	})

	t.Run("deny with empty contexts matches all contexts", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "test",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
						{
							Effect: api.RuleEffectDeny,
							Tools:  []string{"delete_*"},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		// Delete in any context should be denied
		allowed, _ := eval.Evaluate(AuthzRequest{Payload: map[string]any{}, Tool: "delete_resource", Context: "anything"})
		if allowed {
			t.Error("delete should be denied in all contexts")
		}

		// Get should still work
		allowed, _ = eval.Evaluate(AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "anything"})
		if !allowed {
			t.Error("get should be allowed")
		}
	})

	t.Run("deny with only resource filter", func(t *testing.T) {
		config := &api.AuthorizationConfig{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "test",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
							Resources: []api.ResourceRule{
								{Groups: []string{"*"}, Resources: []string{"*"}},
							},
						},
						{
							Effect: api.RuleEffectDeny,
							Resources: []api.ResourceRule{
								{Groups: []string{""}, Resources: []string{"secrets"}},
							},
						},
					},
				},
			},
		}

		eval, err := NewEvaluator(config)
		if err != nil {
			t.Fatalf("NewEvaluator: %v", err)
		}

		// Secrets denied in any context with any tool
		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload:   map[string]any{},
			Tool:      "get_resource",
			Context:   "anything",
			Namespace: "default",
			Resource:  ResourceInfo{Group: "", Resource: "secrets"},
		})
		if allowed {
			t.Error("secrets should be denied")
		}

		// ConfigMaps allowed
		allowed, _ = eval.Evaluate(AuthzRequest{
			Payload:   map[string]any{},
			Tool:      "get_resource",
			Context:   "anything",
			Namespace: "default",
			Resource:  ResourceInfo{Group: "", Resource: "configmaps"},
		})
		if !allowed {
			t.Error("configmaps should be allowed")
		}
	})
}

// ============================================================================
// GVR glob patterns stress test
// ============================================================================

func TestGVRGlobPatterns(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: true,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "test",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{
								Groups:     []string{"*.k8s.io"},
								Versions:   []string{"v1*"},
								Resources:  []string{"*"},
								Namespaces: []string{"prod-*", "staging-*"},
							},
							{
								Groups:     []string{""},
								Resources:  []string{"pods", "services", "configmaps"},
								Namespaces: []string{"*"},
							},
							{
								Groups:     []string{"apps"},
								Resources:  []string{"deploy*", "stateful*", "daemon*"},
								Namespaces: []string{"*"},
							},
						},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		// *.k8s.io groups
		{"networking.k8s.io in prod-*", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "prod-us", Resource: ResourceInfo{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}}, true},
		{"metrics.k8s.io v1beta1 in prod-*", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "prod-eu", Resource: ResourceInfo{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "podmetrics"}}, true},
		{"networking.k8s.io v2 no match", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "prod-us", Resource: ResourceInfo{Group: "networking.k8s.io", Version: "v2", Resource: "ingresses"}}, false},
		{"networking.k8s.io in default no match", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "default", Resource: ResourceInfo{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}}, false},

		// Core group
		{"pods in any ns", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "whatever", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"services in any ns", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "services"}}, true},
		{"secrets not in list", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets"}}, false},

		// Apps group with glob resources
		{"deployments match deploy*", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}, true},
		{"statefulsets match stateful*", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "statefulsets"}}, true},
		{"daemonsets match daemon*", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "daemonsets"}}, true},
		{"replicasets no match", AuthzRequest{Payload: map[string]any{}, Tool: "get_resource", Context: "c", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "replicasets"}}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Order independence: deny always wins regardless of rule order
// ============================================================================

func TestDenyAlwaysWinsRegardlessOfOrder(t *testing.T) {
	// Test that deny wins even when allow comes after deny
	configs := []*api.AuthorizationConfig{
		{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "deny-first",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectDeny,
							Tools:    []string{"delete_*"},
							Contexts: []string{"production"},
						},
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
					},
				},
			},
		},
		{
			AllowAnonymous: true,
			Policies: []api.AuthorizationPolicy{
				{
					Name:  "allow-first",
					Match: api.MatchConfig{Expression: "true"},
					Rules: []api.AuthorizationRule{
						{
							Effect:   api.RuleEffectAllow,
							Tools:    []string{"*"},
							Contexts: []string{"*"},
						},
						{
							Effect:   api.RuleEffectDeny,
							Tools:    []string{"delete_*"},
							Contexts: []string{"production"},
						},
					},
				},
			},
		},
	}

	for i, config := range configs {
		t.Run(fmt.Sprintf("config_%d", i), func(t *testing.T) {
			eval, err := NewEvaluator(config)
			if err != nil {
				t.Fatalf("NewEvaluator: %v", err)
			}

			// Delete in prod should ALWAYS be denied
			allowed, _ := eval.Evaluate(AuthzRequest{
				Payload: map[string]any{},
				Tool:    "delete_resource",
				Context: "production",
			})
			if allowed {
				t.Error("delete in prod should be denied regardless of rule order")
			}

			// Get in prod should be allowed
			allowed, _ = eval.Evaluate(AuthzRequest{
				Payload: map[string]any{},
				Tool:    "get_resource",
				Context: "production",
			})
			if !allowed {
				t.Error("get in prod should be allowed")
			}
		})
	}
}

// ============================================================================
// Deny across policies (rules from different policies are merged)
// ============================================================================

func TestDenyAcrossPolicies(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: true,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "policy-allow",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
					},
				},
			},
			{
				Name:  "policy-deny",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectDeny,
						Tools:    []string{"exec_command"},
						Contexts: []string{"production"},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	allowed, _ := eval.Evaluate(AuthzRequest{
		Payload: map[string]any{},
		Tool:    "exec_command",
		Context: "production",
	})
	if allowed {
		t.Error("deny from another policy should still deny")
	}

	allowed, _ = eval.Evaluate(AuthzRequest{
		Payload: map[string]any{},
		Tool:    "exec_command",
		Context: "staging",
	})
	if !allowed {
		t.Error("exec in staging should be allowed")
	}
}

// ============================================================================
// Default deny: no allow rule matches
// ============================================================================

func TestDefaultDeny(t *testing.T) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "limited-access",
				Match: api.MatchConfig{Expression: `has(payload.sub)`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"get_resource"},
						Contexts: []string{"development"},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	payload := map[string]any{"sub": "user"}

	scenarios := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"allowed combination", AuthzRequest{Payload: payload, Tool: "get_resource", Context: "development"}, true},
		{"wrong tool", AuthzRequest{Payload: payload, Tool: "list_resources", Context: "development"}, false},
		{"wrong context", AuthzRequest{Payload: payload, Tool: "get_resource", Context: "production"}, false},
		{"both wrong", AuthzRequest{Payload: payload, Tool: "delete_resource", Context: "production"}, false},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(s.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != s.want {
				t.Errorf("got %v, want %v", allowed, s.want)
			}
		})
	}
}

// ============================================================================
// Benchmark
// ============================================================================

func BenchmarkEvaluate(b *testing.B) {
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "sre",
				Match: api.MatchConfig{Expression: `"sre" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"*"}, Resources: []string{"*"}},
						},
					},
				},
			},
			{
				Name:  "devs-read",
				Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"get_*", "list_*"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"", "apps"}, Resources: []string{"*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets"}},
						},
					},
				},
			},
			{
				Name:  "devs-write",
				Match: api.MatchConfig{Expression: `"devs" in payload.groups`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"apply_*", "patch_*", "scale_*"},
						Contexts: []string{"dev-*", "staging-*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"apps"}, Resources: []string{"deploy*"}, Namespaces: []string{"team-*"}},
						},
					},
				},
			},
			{
				Name:  "no-exec-prod",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectDeny,
						Tools:    []string{"exec_command"},
						Contexts: []string{"prod-*"},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		b.Fatalf("NewEvaluator: %v", err)
	}

	req := AuthzRequest{
		Payload:   map[string]any{"sub": "dev", "groups": []any{"devs"}},
		Tool:      "get_resource",
		Context:   "prod-us-east",
		Namespace: "team-backend",
		Resource:  ResourceInfo{Group: "apps", Version: "v1", Resource: "deployments", Name: "api-server"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eval.Evaluate(req)
	}
}
