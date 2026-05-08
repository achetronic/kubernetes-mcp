//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// Integration tests for read tools: get_resource, list_resources, describe_resource.
package k8stools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestE2E_GetResource_NotFound(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleGetResource(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"version":   "v1",
		"resource":  "configmaps",
		"name":      "does-not-exist",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected NotFound error")
	requireContains(t, text, "not found", "expected api 'not found' message")
}

func TestE2E_GetResource_WithYQ(t *testing.T) {
	e := newE2EEnv(t)

	e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-yq
  namespace: ` + e.namespace + `
data:
  hello: world
`)

	res, err := e.manager.handleGetResource(context.Background(), makeRequest(map[string]any{
		"context":        e.context,
		"version":        "v1",
		"resource":       "configmaps",
		"name":           "kmcp-e2e-yq",
		"namespace":      e.namespace,
		"yq_expressions": []any{".data.hello"},
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "get_resource with yq")
	if strings.TrimSpace(out) != "world" {
		t.Fatalf("expected 'world', got: %q", out)
	}
}

func TestE2E_ListResources_FiltersByNamespace(t *testing.T) {
	e := newE2EEnv(t)

	for _, n := range []string{"a", "b", "c"} {
		e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-list-` + n + `
  namespace: ` + e.namespace + `
  labels:
    kmcp-e2e: list
`)
	}

	// Use a label_selector to ignore auto-created ConfigMaps like
	// kube-root-ca.crt that the controller-manager places in every namespace.
	res, err := e.manager.handleListResources(context.Background(), makeRequest(map[string]any{
		"context":        e.context,
		"version":        "v1",
		"resource":       "configmaps",
		"namespace":      e.namespace,
		"label_selector": "kmcp-e2e=list",
		"yq_expressions": []any{".items | length"},
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_resources")
	if strings.TrimSpace(out) != "3" {
		t.Fatalf("expected exactly 3 ConfigMaps, got: %q", out)
	}
}

func TestE2E_ListResources_LabelSelector(t *testing.T) {
	e := newE2EEnv(t)

	e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-l1
  namespace: ` + e.namespace + `
  labels:
    team: backend
`)
	e.applyManifest(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-l2
  namespace: ` + e.namespace + `
  labels:
    team: frontend
`)

	res, err := e.manager.handleListResources(context.Background(), makeRequest(map[string]any{
		"context":        e.context,
		"version":        "v1",
		"resource":       "configmaps",
		"namespace":      e.namespace,
		"label_selector": "team=backend",
		"yq_expressions": []any{".items[].metadata.name"},
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_resources label selector")
	if strings.TrimSpace(out) != "kmcp-e2e-l1" {
		t.Fatalf("expected only kmcp-e2e-l1, got: %q", out)
	}
}

func TestE2E_DescribeResource_ResolvesKindFromGVR(t *testing.T) {
	e := newE2EEnv(t)

	// Create a Pod that will fail to schedule a container so events appear.
	e.applyManifest(`
apiVersion: v1
kind: Pod
metadata:
  name: kmcp-e2e-bad
  namespace: ` + e.namespace + `
spec:
  containers:
  - name: x
    image: this-image-does-not-exist:nope
`)

	// Wait for events to be created.
	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	waitForCondition(t, 60*time.Second, func() bool {
		evs, err := cli.Clientset.CoreV1().Events(e.namespace).List(context.Background(), metav1Options())
		if err != nil {
			return false
		}
		for _, ev := range evs.Items {
			if ev.InvolvedObject.Kind == "Pod" && ev.InvolvedObject.Name == "kmcp-e2e-bad" {
				return true
			}
		}
		return false
	})

	res, err := e.manager.handleDescribeResource(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"version":   "v1",
		"resource":  "pods",
		"name":      "kmcp-e2e-bad",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "describe_resource")

	// Must include the resource itself
	requireContains(t, out, "kind: Pod", "expected resource Kind in output")
	// Must include the events section (Kind resolved via RESTMapper)
	requireContains(t, out, "Related Events", "expected events section")
	requireContains(t, out, "kmcp-e2e-bad", "expected involvedObject reference")
}
