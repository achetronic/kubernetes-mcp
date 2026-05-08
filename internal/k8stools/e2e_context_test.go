//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// E2E tests for context tools: get_current_context, list_contexts, switch_context.
package k8stools

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"kubernetes-mcp/api"
	"kubernetes-mcp/internal/authorization"
	"kubernetes-mcp/internal/kubernetes"

	"github.com/mark3labs/mcp-go/server"
)

// newMultiContextEnv builds an env exposing two MCP-level contexts that point
// at the same underlying kubeconfig. This is enough to exercise list_contexts
// and switch_context without needing a second cluster.
func newMultiContextEnv(t *testing.T) (*e2eEnv, string) {
	t.Helper()

	primary := e2eContextName(t)
	secondary := primary + "-alias"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	kCfg := &api.KubernetesConfig{
		DefaultContext: primary,
		Contexts: []api.KubernetesContextConfig{
			{
				Name:              primary,
				Kubeconfig:        kubeconfigPath(),
				KubeconfigContext: primary,
				Description:       "primary",
			},
			{
				Name:              secondary,
				Kubeconfig:        kubeconfigPath(),
				KubeconfigContext: primary, // same physical cluster
				Description:       "alias of primary",
			},
		},
	}

	cm, err := kubernetes.NewClientManager(logger, kCfg)
	if err != nil {
		t.Fatalf("client manager: %v", err)
	}
	t.Cleanup(func() { cm.Stop() })

	authzCfg := &api.AuthorizationConfig{
		AllowAnonymous: true,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "allow-all",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{{
					Effect:   api.RuleEffectAllow,
					Tools:    []string{"*"},
					Contexts: []string{"*"},
				}},
			},
		},
	}
	authz, err := authorization.NewEvaluator(authzCfg)
	if err != nil {
		t.Fatalf("authz: %v", err)
	}

	mcpServer := server.NewMCPServer("kmcp-e2e", "0.0.0", server.WithToolCapabilities(true))
	mgr := NewManager(ManagerDependencies{
		Logger:        logger,
		Config:        &api.Configuration{Kubernetes: *kCfg},
		ClientManager: cm,
		Authz:         authz,
		McpServer:     mcpServer,
	})

	ns := randomNamespace(t)
	createNamespace(t, cm, primary, ns)
	t.Cleanup(func() { deleteNamespace(cm, primary, ns) })

	return &e2eEnv{
		t:             t,
		manager:       mgr,
		clientManager: cm,
		context:       primary,
		namespace:     ns,
	}, secondary
}

func TestE2E_GetCurrentContext(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleGetCurrentContext(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "get_current_context")
	requireContains(t, out, "name: "+e.context, "expected current context name")
}

func TestE2E_ListContexts_ShowsAllAndCurrent(t *testing.T) {
	env, alias := newMultiContextEnv(t)

	res, err := env.manager.handleListContexts(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_contexts")
	requireContains(t, out, "name: "+env.context, "expected primary context")
	requireContains(t, out, "name: "+alias, "expected secondary context")
	// The primary should be marked as current=true. The alias should have current=false.
	if !strings.Contains(out, "current: true") {
		t.Fatalf("expected exactly one context marked current; got:\n%s", out)
	}
}

func TestE2E_SwitchContext(t *testing.T) {
	env, alias := newMultiContextEnv(t)

	// Switch to alias.
	res, err := env.manager.handleSwitchContext(context.Background(), makeRequest(map[string]any{
		"context_name": alias,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "switch_context")
	requireContains(t, out, "Switched context", "expected switch confirmation")
	requireContains(t, out, alias, "expected new context in message")

	// Verify get_current_context now reports the alias.
	res2, _ := env.manager.handleGetCurrentContext(context.Background(), makeRequest(map[string]any{}))
	out2 := expectOK(t, res2, "get_current_context after switch")
	requireContains(t, out2, "name: "+alias, "expected switched context")
}

func TestE2E_SwitchContext_UnknownIsRejected(t *testing.T) {
	env, _ := newMultiContextEnv(t)

	res, err := env.manager.handleSwitchContext(context.Background(), makeRequest(map[string]any{
		"context_name": "nope-this-does-not-exist",
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected error for unknown context")
	requireContains(t, text, "not found", "expected not-found error")
}
