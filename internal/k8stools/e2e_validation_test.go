//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// Integration tests for input validation:
//   - validateGVR rejects empty/uppercase resource and missing version.
//   - undo_rollout only accepts apps/{deployments,statefulsets,daemonsets}.
package k8stools

import (
	"context"
	"testing"
)

func TestE2E_ValidateGVR_RejectsKind(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleListResources(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"version":   "v1",
		"resource":  "Pod", // Capital -> should be rejected
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected validation error for Kind in resource")
	requireContains(t, text, "lowercase plural", "expected validation message")
}

func TestE2E_ValidateGVR_MissingVersion(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleListResources(context.Background(), makeRequest(map[string]any{
		"context":  e.context,
		"resource": "pods",
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected validation error for missing version")
	requireContains(t, text, "version", "expected version-required message")
}

func TestE2E_ValidateGVR_MissingResource(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleListResources(context.Background(), makeRequest(map[string]any{
		"context": e.context,
		"version": "v1",
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected validation error for missing resource")
	requireContains(t, text, "resource", "expected resource-required message")
}

// Regression: undo_rollout only supports the apps group. The previous code
// also accepted (incorrectly) gvr.Group == "" because of an OR; this test
// guarantees that path stays closed.
func TestE2E_UndoRollout_RejectsNonAppsGroup(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "foo.example.com",
		"version":   "v1",
		"resource":  "deployments",
		"name":      "whatever",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "undo_rollout with bogus group should be rejected")
	requireContains(t, text, "only supported for apps/", "expected apps-only message")
}

func TestE2E_UndoRollout_RejectsUnsupportedResource(t *testing.T) {
	e := newE2EEnv(t)

	// ReplicaSets, Jobs, Pods, etc. have no rollout history and must be rejected.
	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "replicasets",
		"name":      "whatever",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "undo_rollout for replicasets should be rejected")
	requireContains(t, text, "deployments,statefulsets,daemonsets", "expected supported-resources message")
}

// scale_resource must reject DaemonSets explicitly: 'spec.replicas' is silently
// ignored by the controller, so allowing the patch would be a footgun.
func TestE2E_ScaleResource_RejectsDaemonSet(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleScaleResource(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "daemonsets",
		"name":      "whatever",
		"namespace": e.namespace,
		"replicas":  float64(3),
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "scale_resource for daemonsets should be rejected")
	requireContains(t, text, "deployments,statefulsets,replicasets", "expected supported-resources message")
}

// scale_resource must reject any non-apps group too.
func TestE2E_ScaleResource_RejectsNonAppsGroup(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleScaleResource(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "batch",
		"version":   "v1",
		"resource":  "jobs",
		"name":      "whatever",
		"namespace": e.namespace,
		"replicas":  float64(3),
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "scale_resource for batch/jobs should be rejected")
	requireContains(t, text, "only supported for apps/", "expected apps-only message")
}

// get_rollout_status must reject kinds outside the apps/v1 rollout family.
func TestE2E_GetRolloutStatus_RejectsReplicaSet(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleGetRolloutStatus(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "replicasets",
		"name":      "whatever",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "get_rollout_status for replicasets should be rejected")
	requireContains(t, text, "deployments,statefulsets,daemonsets", "expected supported-resources message")
}
