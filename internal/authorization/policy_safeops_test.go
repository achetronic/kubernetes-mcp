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

func buildSafeOperationsEvaluator(t *testing.T) *Evaluator {
	t.Helper()
	config := &api.AuthorizationConfig{
		AllowAnonymous: false,
		Policies: []api.AuthorizationPolicy{
			{
				Name:        "safe-operations",
				Description: "Read-only + delete pods. No apply, no patch, no exec, no sensitive reads",
				Match:       api.MatchConfig{Expression: `has(payload.sub)`},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"get_*", "list_*", "describe_*", "diff_*", "check_*", "scale_*", "*_rollout*", "get_logs"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{"*"}, Resources: []string{"*"}},
							{Groups: []string{"_"}, Resources: []string{"*"}},
						},
					},
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"delete_resource"},
						Contexts: []string{"*"},
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"pods"}, Namespaces: []string{"aplicacion-*", "default"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Tools:  []string{"get_*", "list_*", "describe_*"},
						Resources: []api.ResourceRule{
							{Groups: []string{""}, Resources: []string{"secrets", "serviceaccounts"}},
							{Groups: []string{"external-secrets.io", "cert-manager.io", "certificates.k8s.io"}, Resources: []string{"*"}},
						},
					},
					{
						Effect: api.RuleEffectDeny,
						Tools:  []string{"exec_command"},
					},
				},
			},
		},
	}

	eval, err := NewEvaluator(config)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	return eval
}

// ============================================================================
// Normal read operations on non-sensitive resources
// ============================================================================

func TestSafeOps_ReadNonSensitive(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	cases := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		// Core group resources
		{"get pods", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "nginx-abc"}}, true},
		{"list pods", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"describe pod", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "nginx-abc"}}, true},
		{"get configmap", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "configmaps", Name: "app-config"}}, true},
		{"list configmaps", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "configmaps"}}, true},
		{"get services", AuthzRequest{Payload: p, Tool: "get_resource", Context: "staging", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "services", Name: "api-svc"}}, true},
		{"list persistentvolumeclaims", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "data", Resource: ResourceInfo{Group: "", Resource: "persistentvolumeclaims"}}, true},
		{"get endpoints", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "endpoints", Name: "api-ep"}}, true},
		{"list events", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "events"}}, true},
		{"describe node (cluster-scoped)", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "", Resource: "nodes", Name: "node-1"}}, true},
		{"get namespaces", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "", Resource: "namespaces", Name: "default"}}, true},

		// Apps group
		{"get deployment", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api-server"}}, true},
		{"list statefulsets", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "data", Resource: ResourceInfo{Group: "apps", Resource: "statefulsets"}}, true},
		{"describe daemonset", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "prod", Namespace: "monitoring", Resource: ResourceInfo{Group: "apps", Resource: "daemonsets", Name: "fluentd"}}, true},
		{"get replicaset", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "replicasets", Name: "api-rs"}}, true},

		// Batch group
		{"get job", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "jobs", Resource: ResourceInfo{Group: "batch", Resource: "jobs", Name: "migration-job"}}, true},
		{"list cronjobs", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "cron", Resource: ResourceInfo{Group: "batch", Resource: "cronjobs"}}, true},

		// Networking
		{"get ingress", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "networking.k8s.io", Resource: "ingresses", Name: "main-ingress"}}, true},
		{"list networkpolicies", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "secure", Resource: ResourceInfo{Group: "networking.k8s.io", Resource: "networkpolicies"}}, true},

		// Metrics
		{"get pod metrics", AuthzRequest{Payload: p, Tool: "get_pod_metrics", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "metrics.k8s.io", Resource: "podmetrics"}}, true},
		{"get node metrics", AuthzRequest{Payload: p, Tool: "get_node_metrics", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "metrics.k8s.io", Resource: "nodemetrics"}}, true},

		// RBAC resources (reading is fine, not in deny list)
		{"get clusterroles", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Name: "admin"}}, true},
		{"list rolebindings", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "rbac.authorization.k8s.io", Resource: "rolebindings"}}, true},

		// CRDs (arbitrary groups)
		{"get prometheus", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "monitoring", Resource: ResourceInfo{Group: "monitoring.coreos.com", Resource: "prometheuses", Name: "main"}}, true},
		{"list servicemonitors", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "monitoring", Resource: ResourceInfo{Group: "monitoring.coreos.com", Resource: "servicemonitors"}}, true},
		{"get virtualservices", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "networking.istio.io", Resource: "virtualservices", Name: "api-vs"}}, true},

		// Multiple contexts
		{"read in staging", AuthzRequest{Payload: p, Tool: "get_resource", Context: "staging", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"read in dev", AuthzRequest{Payload: p, Tool: "get_resource", Context: "development", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
		{"read in dr", AuthzRequest{Payload: p, Tool: "get_resource", Context: "disaster-recovery", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(c.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != c.want {
				t.Errorf("got %v, want %v", allowed, c.want)
			}
		})
	}
}

// ============================================================================
// Sensitive data reads: ALL DENIED
// ============================================================================

func TestSafeOps_SensitiveReadsDenied(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	cases := []struct {
		name string
		req  AuthzRequest
	}{
		// Secrets
		{"get secret default ns", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "db-password"}}},
		{"list secrets default ns", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets"}}},
		{"describe secret", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "tls-cert"}}},
		{"get secret kube-system", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "coredns-token"}}},
		{"list secrets all namespaces", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "", Resource: "secrets"}}},
		{"get secret in staging", AuthzRequest{Payload: p, Tool: "get_resource", Context: "staging", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "api-key"}}},
		{"get secret in team ns", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "jwt-key"}}},

		// ServiceAccounts
		{"get serviceaccount", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "serviceaccounts", Name: "default"}}},
		{"list serviceaccounts", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "serviceaccounts"}}},
		{"describe serviceaccount", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "staging", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "serviceaccounts", Name: "admin-sa"}}},

		// External Secrets
		{"get externalsecret", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "external-secrets.io", Resource: "externalsecrets", Name: "db-creds"}}},
		{"list externalsecrets", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "external-secrets.io", Resource: "externalsecrets"}}},
		{"get secretstore", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "external-secrets.io", Resource: "secretstores", Name: "vault"}}},
		{"list clustersecretstores", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "external-secrets.io", Resource: "clustersecretstores"}}},
		{"describe pushsecret", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "external-secrets.io", Resource: "pushsecrets", Name: "sync"}}},

		// cert-manager
		{"get certificate", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "certificates", Name: "wildcard-tls"}}},
		{"list certificates", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "istio-system", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "certificates"}}},
		{"get issuer", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "issuers", Name: "letsencrypt"}}},
		{"list clusterissuers", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "clusterissuers"}}},
		{"describe certificaterequest", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "certificaterequests", Name: "req-1"}}},
		{"get order", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "orders", Name: "ord-1"}}},
		{"list challenges", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "challenges"}}},

		// certificates.k8s.io
		{"get csr", AuthzRequest{Payload: p, Tool: "get_resource", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "certificates.k8s.io", Resource: "certificatesigningrequests", Name: "csr-1"}}},
		{"list csrs", AuthzRequest{Payload: p, Tool: "list_resources", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "certificates.k8s.io", Resource: "certificatesigningrequests"}}},
		{"describe csr", AuthzRequest{Payload: p, Tool: "describe_resource", Context: "staging", Namespace: "", Resource: ResourceInfo{Group: "certificates.k8s.io", Resource: "certificatesigningrequests", Name: "node-csr"}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(c.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed {
				t.Error("should be DENIED")
			}
		})
	}
}

// ============================================================================
// Exec: ALL DENIED, every context, every namespace, every pod
// ============================================================================

func TestSafeOps_ExecDenied(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	cases := []struct {
		name string
		req  AuthzRequest
	}{
		{"exec in default/prod", AuthzRequest{Payload: p, Tool: "exec_command", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "api-pod"}}},
		{"exec in kube-system", AuthzRequest{Payload: p, Tool: "exec_command", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "coredns"}}},
		{"exec in team ns", AuthzRequest{Payload: p, Tool: "exec_command", Context: "prod", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "debug"}}},
		{"exec in staging", AuthzRequest{Payload: p, Tool: "exec_command", Context: "staging", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "test"}}},
		{"exec in dev", AuthzRequest{Payload: p, Tool: "exec_command", Context: "dev", Namespace: "dev-test", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "dev-pod"}}},
		{"exec in dr", AuthzRequest{Payload: p, Tool: "exec_command", Context: "disaster-recovery", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "dr-pod"}}},
		{"exec with no namespace", AuthzRequest{Payload: p, Tool: "exec_command", Context: "prod", Namespace: "", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "orphan"}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(c.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed {
				t.Error("should be DENIED")
			}
		})
	}
}

// ============================================================================
// Delete pods: ONLY in aplicacion-* and default, ONLY pods, ONLY single delete
// ============================================================================

func TestSafeOps_DeletePods(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	t.Run("allowed_matching_namespaces", func(t *testing.T) {
		cases := []struct {
			name string
			req  AuthzRequest
		}{
			{"delete pod aplicacion-01", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "aplicacion-01", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "stuck-pod"}}},
			{"delete pod aplicacion-02", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "aplicacion-02", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "oom-pod"}}},
			{"delete pod aplicacion-03", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "staging", Namespace: "aplicacion-03", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "crash-pod"}}},
			{"delete pod aplicacion-99", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "aplicacion-99", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "future-pod"}}},
			{"delete pod default", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "pod-1"}}},
			{"delete pod default dr ctx", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "disaster-recovery", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "dr-pod"}}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				allowed, err := eval.Evaluate(c.req)
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				if !allowed {
					t.Error("should be ALLOWED")
				}
			})
		}
	})

	t.Run("denied_wrong_namespace", func(t *testing.T) {
		cases := []struct {
			name string
			req  AuthzRequest
		}{
			{"delete pod kube-system", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "coredns"}}},
			{"delete pod kube-public", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "kube-public", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "info"}}},
			{"delete pod istio", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "istio", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "istiod"}}},
			{"delete pod admitik", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "admitik", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "webhook"}}},
			{"delete pod local-path-storage", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "local-path-storage", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "provisioner"}}},
			{"delete pod kube-node-lease", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "kube-node-lease", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "lease"}}},
			{"delete pod monitoring", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "monitoring", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "prom"}}},
			{"delete pod random-ns", AuthzRequest{Payload: p, Tool: "delete_resource", Context: "prod", Namespace: "random-namespace", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "rnd"}}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				allowed, err := eval.Evaluate(c.req)
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				if allowed {
					t.Error("should be DENIED")
				}
			})
		}
	})

	t.Run("denied_wrong_resource_type", func(t *testing.T) {
		resources := []struct {
			group    string
			resource string
			name     string
		}{
			{"apps", "deployments", "api-server"},
			{"apps", "statefulsets", "redis"},
			{"apps", "daemonsets", "fluentd"},
			{"apps", "replicasets", "api-rs"},
			{"", "services", "api-svc"},
			{"", "configmaps", "app-config"},
			{"", "secrets", "db-password"},
			{"", "serviceaccounts", "admin-sa"},
			{"", "persistentvolumeclaims", "data-pvc"},
			{"", "endpoints", "api-ep"},
			{"", "namespaces", "team-backend"},
			{"batch", "jobs", "migration"},
			{"batch", "cronjobs", "cleanup"},
			{"networking.k8s.io", "ingresses", "main"},
			{"networking.k8s.io", "networkpolicies", "deny-all"},
			{"cert-manager.io", "certificates", "wildcard"},
			{"external-secrets.io", "externalsecrets", "db-creds"},
			{"rbac.authorization.k8s.io", "clusterroles", "admin"},
			{"monitoring.coreos.com", "prometheuses", "main"},
		}
		for _, r := range resources {
			t.Run("delete_"+r.resource, func(t *testing.T) {
				allowed, err := eval.Evaluate(AuthzRequest{
					Payload:   p,
					Tool:      "delete_resource",
					Context:   "prod",
					Namespace: "default",
					Resource:  ResourceInfo{Group: r.group, Resource: r.resource, Name: r.name},
				})
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				if allowed {
					t.Errorf("delete %s/%s should be DENIED", r.group, r.resource)
				}
			})
		}
	})

	t.Run("denied_bulk_delete", func(t *testing.T) {
		cases := []struct {
			name string
			req  AuthzRequest
		}{
			{"bulk delete pods default", AuthzRequest{Payload: p, Tool: "delete_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods"}}},
			{"bulk delete pods aplicacion-01", AuthzRequest{Payload: p, Tool: "delete_resources", Context: "prod", Namespace: "aplicacion-01", Resource: ResourceInfo{Group: "", Resource: "pods"}}},
			{"bulk delete pods kube-system", AuthzRequest{Payload: p, Tool: "delete_resources", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "pods"}}},
			{"bulk delete deployments default", AuthzRequest{Payload: p, Tool: "delete_resources", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments"}}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				allowed, err := eval.Evaluate(c.req)
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				if allowed {
					t.Error("should be DENIED")
				}
			})
		}
	})
}

// ============================================================================
// Write operations: apply/patch DENIED, scale/rollout ALLOWED
// ============================================================================

func TestSafeOps_WriteOperations(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	namespaces := []string{"default", "team-backend", "kube-system", "monitoring", "staging-api", "dev-test"}
	contexts := []string{"prod", "staging", "development", "disaster-recovery"}

	resources := []struct {
		group    string
		resource string
		name     string
	}{
		{"apps", "deployments", "api-server"},
		{"apps", "statefulsets", "redis"},
		{"apps", "daemonsets", "fluentd"},
		{"", "configmaps", "app-config"},
		{"", "services", "api-svc"},
		{"", "persistentvolumeclaims", "data-pvc"},
		{"batch", "jobs", "migration"},
		{"batch", "cronjobs", "cleanup"},
		{"networking.k8s.io", "ingresses", "main"},
		{"", "secrets", "db-password"},
		{"", "serviceaccounts", "app-sa"},
		{"cert-manager.io", "certificates", "wildcard"},
		{"external-secrets.io", "externalsecrets", "db-creds"},
	}

	t.Run("apply_patch_denied", func(t *testing.T) {
		deniedTools := []string{"apply_manifest", "patch_resource"}
		for _, tool := range deniedTools {
			for _, ctx := range contexts {
				for _, ns := range namespaces {
					for _, r := range resources {
						t.Run(tool+"/"+ctx+"/"+ns+"/"+r.resource, func(t *testing.T) {
							allowed, err := eval.Evaluate(AuthzRequest{
								Payload:   p,
								Tool:      tool,
								Context:   ctx,
								Namespace: ns,
								Resource:  ResourceInfo{Group: r.group, Resource: r.resource, Name: r.name},
							})
							if err != nil {
								t.Fatalf("Evaluate: %v", err)
							}
							if allowed {
								t.Errorf("should be DENIED")
							}
						})
					}
				}
			}
		}
	})

	t.Run("scale_rollout_allowed", func(t *testing.T) {
		allowedTools := []string{"scale_resource", "restart_rollout", "undo_rollout"}
		for _, tool := range allowedTools {
			for _, ctx := range contexts {
				for _, ns := range namespaces {
					for _, r := range resources {
						t.Run(tool+"/"+ctx+"/"+ns+"/"+r.resource, func(t *testing.T) {
							allowed, err := eval.Evaluate(AuthzRequest{
								Payload:   p,
								Tool:      tool,
								Context:   ctx,
								Namespace: ns,
								Resource:  ResourceInfo{Group: r.group, Resource: r.resource, Name: r.name},
							})
							if err != nil {
								t.Fatalf("Evaluate: %v", err)
							}
							if !allowed {
								t.Errorf("should be ALLOWED")
							}
						})
					}
				}
			}
		}
	})
}

// ============================================================================
// Read-like tools: get_logs, diff_manifest, check_permission, rollout status
// ============================================================================

func TestSafeOps_ReadLikeTools(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	cases := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"get_logs", AuthzRequest{Payload: p, Tool: "get_logs", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "api-pod"}}, true},
		{"get_logs kube-system", AuthzRequest{Payload: p, Tool: "get_logs", Context: "prod", Namespace: "kube-system", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "coredns"}}, true},
		{"get_logs staging", AuthzRequest{Payload: p, Tool: "get_logs", Context: "staging", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "pods", Name: "debug"}}, true},
		{"diff_manifest", AuthzRequest{Payload: p, Tool: "diff_manifest", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"check_permission", AuthzRequest{Payload: p, Tool: "check_permission", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "authorization.k8s.io", Resource: "selfsubjectaccessreviews"}}, true},
		{"get_rollout_status", AuthzRequest{Payload: p, Tool: "get_rollout_status", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"restart_rollout", AuthzRequest{Payload: p, Tool: "restart_rollout", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}, true},
		{"undo_rollout", AuthzRequest{Payload: p, Tool: "undo_rollout", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "statefulsets", Name: "redis"}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(c.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != c.want {
				t.Errorf("got %v, want %v", allowed, c.want)
			}
		})
	}
}

// ============================================================================
// Virtual MCP resources
// ============================================================================

func TestSafeOps_VirtualResources(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	cases := []struct {
		name string
		req  AuthzRequest
		want bool
	}{
		{"list_contexts", AuthzRequest{Payload: p, Tool: "list_contexts", Context: ""}, true},
		{"get_current_context", AuthzRequest{Payload: p, Tool: "get_current_context", Context: ""}, true},
		{"get_cluster_info", AuthzRequest{Payload: p, Tool: "get_cluster_info", Context: "prod"}, true},
		{"list_api_resources", AuthzRequest{Payload: p, Tool: "list_api_resources", Context: "prod"}, true},
		{"list_api_versions", AuthzRequest{Payload: p, Tool: "list_api_versions", Context: "prod"}, true},
		// switch_context not in allow tools list (doesn't match get_*, list_*, etc.)
		{"switch_context denied", AuthzRequest{Payload: p, Tool: "switch_context", Context: "staging"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(c.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != c.want {
				t.Errorf("got %v, want %v", allowed, c.want)
			}
		})
	}
}

// ============================================================================
// Authentication: anonymous denied, missing sub denied
// ============================================================================

func TestSafeOps_Authentication(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)

	cases := []struct {
		name    string
		payload map[string]any
		want    bool
	}{
		{"nil payload", nil, false},
		{"empty payload", map[string]any{}, false},
		{"no sub claim", map[string]any{"email": "user@co.com"}, false},
		{"with sub claim", map[string]any{"sub": "user"}, true},
		{"sub + groups", map[string]any{"sub": "user", "groups": []any{"devs"}}, true},
		{"sub + email", map[string]any{"sub": "user", "email": "user@co.com"}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(AuthzRequest{
				Payload:   c.payload,
				Tool:      "get_resource",
				Context:   "prod",
				Namespace: "default",
				Resource:  ResourceInfo{Group: "", Resource: "pods"},
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed != c.want {
				t.Errorf("got %v, want %v", allowed, c.want)
			}
		})
	}
}

// ============================================================================
// Tools that are NOT in any allow list: all denied
// ============================================================================

func TestSafeOps_UnlistedToolsDenied(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	tools := []string{
		"switch_context",
		"unknown_tool",
		"admin_panel",
		"set_labels",
		"set_annotations",
		"remove_labels",
		"remove_annotations",
		"cordon_node",
		"drain_node",
		"taint_node",
		"apply_manifest",
		"patch_resource",
	}

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			allowed, err := eval.Evaluate(AuthzRequest{
				Payload:   p,
				Tool:      tool,
				Context:   "prod",
				Namespace: "default",
				Resource:  ResourceInfo{Group: "", Resource: "pods"},
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed {
				t.Errorf("tool %s should be DENIED", tool)
			}
		})
	}
}

// ============================================================================
// Cross-cutting: apply/patch are denied even on non-sensitive resources
// ============================================================================

func TestSafeOps_ApplyPatchDeniedEverywhere(t *testing.T) {
	eval := buildSafeOperationsEvaluator(t)
	p := map[string]any{"sub": "user@company.com"}

	cases := []struct {
		name string
		req  AuthzRequest
	}{
		{"apply deployment", AuthzRequest{Payload: p, Tool: "apply_manifest", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}},
		{"patch deployment", AuthzRequest{Payload: p, Tool: "patch_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "apps", Resource: "deployments", Name: "api"}}},
		{"apply configmap", AuthzRequest{Payload: p, Tool: "apply_manifest", Context: "staging", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "configmaps", Name: "cfg"}}},
		{"patch service", AuthzRequest{Payload: p, Tool: "patch_resource", Context: "prod", Namespace: "monitoring", Resource: ResourceInfo{Group: "", Resource: "services", Name: "svc"}}},
		{"apply secret", AuthzRequest{Payload: p, Tool: "apply_manifest", Context: "prod", Namespace: "team-backend", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "new-secret"}}},
		{"patch secret", AuthzRequest{Payload: p, Tool: "patch_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "", Resource: "secrets", Name: "tls-cert"}}},
		{"apply externalsecret", AuthzRequest{Payload: p, Tool: "apply_manifest", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "external-secrets.io", Resource: "externalsecrets", Name: "db-creds"}}},
		{"patch certificate", AuthzRequest{Payload: p, Tool: "patch_resource", Context: "prod", Namespace: "default", Resource: ResourceInfo{Group: "cert-manager.io", Resource: "certificates", Name: "wildcard"}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allowed, err := eval.Evaluate(c.req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if allowed {
				t.Error("should be DENIED")
			}
		})
	}
}
