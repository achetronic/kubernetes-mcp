//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// E2E tests for cluster discovery / inspection tools:
// list_namespaces, get_cluster_info, list_api_resources, list_api_versions.
package k8stools

import (
	"context"
	"strings"
	"testing"
)

func TestE2E_ListNamespaces_IncludesTestNamespace(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleListNamespaces(context.Background(), makeRequest(map[string]any{
		"context": e.context,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_namespaces")
	requireContains(t, out, e.namespace, "expected test namespace in list")
	requireContains(t, out, "kube-system", "expected kube-system in list")
}

func TestE2E_ListNamespaces_LabelSelector(t *testing.T) {
	e := newE2EEnv(t)

	// Label our test namespace and a second one to verify filtering.
	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	ns, err := cli.Clientset.CoreV1().Namespaces().Get(context.Background(), e.namespace, metav1Get())
	if err != nil {
		t.Fatalf("get ns: %v", err)
	}
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	ns.Labels["kmcp-e2e-test"] = "yes"
	if _, err := cli.Clientset.CoreV1().Namespaces().Update(context.Background(), ns, metav1Update()); err != nil {
		t.Fatalf("update ns labels: %v", err)
	}

	res, err := e.manager.handleListNamespaces(context.Background(), makeRequest(map[string]any{
		"context":        e.context,
		"label_selector": "kmcp-e2e-test=yes",
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_namespaces label selector")
	requireContains(t, out, e.namespace, "expected labeled namespace")
	if strings.Contains(out, "kube-system") {
		t.Fatalf("expected kube-system to be filtered out; got:\n%s", out)
	}
}

func TestE2E_GetClusterInfo(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleGetClusterInfo(context.Background(), makeRequest(map[string]any{
		"context": e.context,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "get_cluster_info")
	requireContains(t, out, "context: "+e.context, "expected context name")
	requireContains(t, out, "server_version: ", "expected server version")
	requireContains(t, out, "node_count: ", "expected node count")
	requireContains(t, out, "namespace_count: ", "expected namespace count")
}

func TestE2E_ListAPIResources_FilterByGroup(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleListAPIResources(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"api_group": "apps",
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_api_resources apps")
	requireContains(t, out, "kind: Deployment", "expected Deployment in apps group")
	requireContains(t, out, "name: deployments", "expected deployments resource name")
	if strings.Contains(out, "kind: Pod\n") {
		t.Fatalf("expected core resources to be filtered out; got Pod in output:\n%s", out)
	}
}

func TestE2E_ListAPIResources_NamespacedFilter(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleListAPIResources(context.Background(), makeRequest(map[string]any{
		"context":    e.context,
		"namespaced": false,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_api_resources cluster-scoped")
	requireContains(t, out, "name: namespaces", "expected namespaces (cluster-scoped)")
	requireContains(t, out, "name: nodes", "expected nodes (cluster-scoped)")
	if strings.Contains(out, "name: pods\n") {
		t.Fatalf("expected pods (namespaced) to be filtered out; got:\n%s", out)
	}
}

func TestE2E_ListAPIVersions(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleListAPIVersions(context.Background(), makeRequest(map[string]any{
		"context": e.context,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_api_versions")
	requireContains(t, out, "apps", "expected apps API group")
	requireContains(t, out, "networking.k8s.io", "expected networking.k8s.io API group")
}
