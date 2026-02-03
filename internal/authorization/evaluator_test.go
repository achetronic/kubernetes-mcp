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
	"testing"

	"kubernetes-mcp/api"
)

func TestResourceVersionMatching(t *testing.T) {
	tests := []struct {
		name           string
		denyVersions   []string
		resourceVersion string
		expectDenied   bool
	}{
		{
			name:           "deny with v1, resource has v1 - should deny",
			denyVersions:   []string{"v1"},
			resourceVersion: "v1",
			expectDenied:   true,
		},
		{
			name:           "deny with v1, resource has empty version - should NOT deny",
			denyVersions:   []string{"v1"},
			resourceVersion: "",
			expectDenied:   false,
		},
		{
			name:           "deny with wildcard, resource has v1 - should deny",
			denyVersions:   []string{"*"},
			resourceVersion: "v1",
			expectDenied:   true,
		},
		{
			name:           "deny with wildcard, resource has empty version - should deny",
			denyVersions:   []string{"*"},
			resourceVersion: "",
			expectDenied:   true,
		},
		{
			name:           "deny without versions (omitted), resource has v1 - should deny",
			denyVersions:   nil,
			resourceVersion: "v1",
			expectDenied:   true,
		},
		{
			name:           "deny without versions (omitted), resource has empty - should deny",
			denyVersions:   nil,
			resourceVersion: "",
			expectDenied:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &api.AuthorizationConfig{
				AllowAnonymous: true,
				Policies: []api.AuthorizationPolicy{
					{
						Name: "test-policy",
						Match: api.MatchConfig{
							Expression: "true",
						},
						Allow: &api.ToolContextRule{
							Tools:    []string{"*"},
							Contexts: []string{"*"},
							Resources: []api.ResourceRule{
								{
									Groups: []string{"*"},
									Kinds:  []string{"*"},
								},
							},
						},
						Deny: &api.ToolContextRule{
							Resources: []api.ResourceRule{
								{
									Groups:   []string{""},
									Versions: tt.denyVersions,
									Kinds:    []string{"Secret"},
								},
							},
						},
					},
				},
			}

			evaluator, err := NewEvaluator(config)
			if err != nil {
				t.Fatalf("failed to create evaluator: %v", err)
			}

			req := AuthzRequest{
				Payload:   map[string]any{},
				Tool:      "get_resource",
				Context:   "test",
				Namespace: "default",
				Resource: ResourceInfo{
					Group:   "",
					Version: tt.resourceVersion,
					Kind:    "Secret",
					Name:    "my-secret",
				},
			}

			allowed, err := evaluator.Evaluate(req)
			if err != nil {
				t.Fatalf("evaluation error: %v", err)
			}

			if tt.expectDenied && allowed {
				t.Errorf("expected request to be DENIED, but it was ALLOWED")
			}
			if !tt.expectDenied && !allowed {
				t.Errorf("expected request to be ALLOWED, but it was DENIED")
			}
		})
	}
}

func TestResourceVersionMatchingForConfigMap(t *testing.T) {
	// ConfigMap should be allowed regardless of version field
	config := &api.AuthorizationConfig{
		AllowAnonymous: true,
		Policies: []api.AuthorizationPolicy{
			{
				Name: "test-policy",
				Match: api.MatchConfig{
					Expression: "true",
				},
				Allow: &api.ToolContextRule{
					Tools:    []string{"*"},
					Contexts: []string{"*"},
					Resources: []api.ResourceRule{
						{
							Groups: []string{"*"},
							Kinds:  []string{"*"},
						},
					},
				},
				Deny: &api.ToolContextRule{
					Resources: []api.ResourceRule{
						{
							Groups:   []string{""},
							Versions: []string{"v1"},
							Kinds:    []string{"Secret"},
						},
					},
				},
			},
		},
	}

	evaluator, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	// ConfigMap with v1 should be allowed
	req := AuthzRequest{
		Payload:   map[string]any{},
		Tool:      "get_resource",
		Context:   "test",
		Namespace: "default",
		Resource: ResourceInfo{
			Group:   "",
			Version: "v1",
			Kind:    "ConfigMap",
			Name:    "my-config",
		},
	}

	allowed, err := evaluator.Evaluate(req)
	if err != nil {
		t.Fatalf("evaluation error: %v", err)
	}

	if !allowed {
		t.Error("ConfigMap should be ALLOWED, but was DENIED")
	}
}
