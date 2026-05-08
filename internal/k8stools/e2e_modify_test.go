//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// Integration tests for write tools: patch, delete, scale, rollout management.
package k8stools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestE2E_PatchResource_Merge(t *testing.T) {
	e := newE2EEnv(t)

	e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-patch
  namespace: ` + e.namespace + `
data:
  k: original
`)

	res, err := e.manager.handlePatchResource(context.Background(), makeRequest(map[string]any{
		"context":    e.context,
		"version":    "v1",
		"resource":   "configmaps",
		"name":       "kmcp-e2e-patch",
		"namespace":  e.namespace,
		"patch_type": "merge",
		"patch":      `{"data":{"k":"patched","new":"yes"}}`,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "patch_resource")
	requireContains(t, out, "Successfully patched configmaps/kmcp-e2e-patch", "expected success")
	requireContains(t, out, "patched", "expected new value")
	requireContains(t, out, `new: "yes"`, "expected new key")
}

func TestE2E_DeleteResource(t *testing.T) {
	e := newE2EEnv(t)

	e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-del
  namespace: ` + e.namespace + `
`)

	res, err := e.manager.handleDeleteResource(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"version":   "v1",
		"resource":  "configmaps",
		"name":      "kmcp-e2e-del",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	expectOK(t, res, "delete_resource")

	if e.resourceExists("", "v1", "configmaps", "kmcp-e2e-del") {
		t.Fatalf("ConfigMap was not deleted")
	}
}

func TestE2E_DeleteResources_RequiresSelector(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleDeleteResources(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"version":   "v1",
		"resource":  "configmaps",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected selector required error")
	requireContains(t, text, "selector", "expected selector-required error")
}

func TestE2E_DeleteResources_LabelSelector(t *testing.T) {
	e := newE2EEnv(t)

	e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-bulk-keep
  namespace: ` + e.namespace + `
  labels:
    bulk: keep
`)
	e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-bulk-rm
  namespace: ` + e.namespace + `
  labels:
    bulk: remove
`)

	res, err := e.manager.handleDeleteResources(context.Background(), makeRequest(map[string]any{
		"context":        e.context,
		"version":        "v1",
		"resource":       "configmaps",
		"namespace":      e.namespace,
		"label_selector": "bulk=remove",
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	expectOK(t, res, "delete_resources")

	if e.resourceExists("", "v1", "configmaps", "kmcp-e2e-bulk-rm") {
		t.Fatalf("ConfigMap with bulk=remove was not deleted")
	}
	if !e.resourceExists("", "v1", "configmaps", "kmcp-e2e-bulk-keep") {
		t.Fatalf("ConfigMap with bulk=keep should not have been touched")
	}
}

// applyTestDeployment is a small helper used by scale/rollout tests.
func applyTestDeployment(e *e2eEnv, name string) {
	e.applyManifest(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ` + name + `
  template:
    metadata:
      labels:
        app: ` + name + `
    spec:
      containers:
      - name: nginx
        image: nginx:alpine
`)
}

func TestE2E_ScaleResource(t *testing.T) {
	e := newE2EEnv(t)
	applyTestDeployment(e, "kmcp-e2e-scale")

	res, err := e.manager.handleScaleResource(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "deployments",
		"name":      "kmcp-e2e-scale",
		"namespace": e.namespace,
		"replicas":  float64(3),
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "scale_resource")
	requireContains(t, out, "to 3 replicas", "expected scale message")
}

func TestE2E_GetRolloutStatus(t *testing.T) {
	e := newE2EEnv(t)
	applyTestDeployment(e, "kmcp-e2e-status")

	// Wait until status is observed.
	waitForCondition(t, 30*time.Second, func() bool {
		res, err := e.manager.handleGetRolloutStatus(context.Background(), makeRequest(map[string]any{
			"context":   e.context,
			"group":     "apps",
			"version":   "v1",
			"resource":  "deployments",
			"name":      "kmcp-e2e-status",
			"namespace": e.namespace,
		}))
		if err != nil {
			return false
		}
		out, isErr := firstText(res)
		return !isErr && strings.Contains(out, "Rollout Status for")
	})
}

func TestE2E_RestartRollout(t *testing.T) {
	e := newE2EEnv(t)
	applyTestDeployment(e, "kmcp-e2e-restart")

	res, err := e.manager.handleRestartRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "deployments",
		"name":      "kmcp-e2e-restart",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "restart_rollout")
	requireContains(t, out, "Successfully triggered restart", "expected restart message")

	// Verify the restartedAt annotation was applied
	cli, _ := e.clientManager.GetClient(e.context)
	dep, err := cli.DynamicClient.
		Resource(gvrOf("apps", "v1", "deployments")).
		Namespace(e.namespace).
		Get(context.Background(), "kmcp-e2e-restart", metav1Get())
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	annotations, _, _ := nestedMap(dep.Object, "spec", "template", "metadata", "annotations")
	if _, ok := annotations["kubectl.kubernetes.io/restartedAt"]; !ok {
		t.Fatalf("restartedAt annotation not set; got annotations=%v", annotations)
	}
}
