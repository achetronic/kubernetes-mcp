//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// E2E tests for check_permission (SelfSubjectAccessReview).
package k8stools

import (
	"context"
	"testing"
)

func TestE2E_CheckPermission_AllowedForClusterAdmin(t *testing.T) {
	e := newE2EEnv(t)

	// The kubeconfig used by Kind is cluster-admin, so listing pods must be allowed.
	res, err := e.manager.handleCheckPermission(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"verb":      "list",
		"group":     "",
		"resource":  "pods",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "check_permission list pods")
	requireContains(t, out, "Permission check: allowed", "expected allowed")
	requireContains(t, out, "Verb:      list", "expected verb in summary")
	requireContains(t, out, "Resource:  pods", "expected resource in summary")
}

func TestE2E_CheckPermission_NamedResource(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleCheckPermission(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"verb":      "get",
		"group":     "apps",
		"resource":  "deployments",
		"name":      "any-name",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "check_permission with name")
	requireContains(t, out, "Permission check: allowed", "expected allowed")
	requireContains(t, out, "Name:      any-name", "expected name in summary")
	requireContains(t, out, "Namespace: "+e.namespace, "expected namespace in summary")
}

func TestE2E_CheckPermission_UnknownVerbStillReports(t *testing.T) {
	e := newE2EEnv(t)

	// SelfSubjectAccessReview accepts any verb string; unknown verbs typically
	// come back as "denied" rather than as an error.
	res, err := e.manager.handleCheckPermission(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"verb":      "non-existent-verb",
		"group":     "",
		"resource":  "pods",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "check_permission unknown verb")
	requireContains(t, out, "Permission check:", "expected a verdict")
}
