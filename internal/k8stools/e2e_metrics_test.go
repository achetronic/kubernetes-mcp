//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// E2E tests for get_pod_metrics and get_node_metrics. Skipped automatically
// when metrics-server is not installed in the cluster.
package k8stools

import (
	"context"
	"testing"
	"time"
)

func TestE2E_GetPodMetrics_NoMetricsServer_ReportsClearError(t *testing.T) {
	e := newE2EEnv(t)
	if e.metricsServerAvailable() {
		t.Skip("metrics-server is installed; this test only runs when it is missing")
	}

	res, err := e.manager.handleGetPodMetrics(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected error when metrics-server unavailable")
	requireContains(t, text, "metrics-server is not available", "expected clear missing-metrics-server error")
}

func TestE2E_GetNodeMetrics_NoMetricsServer_ReportsClearError(t *testing.T) {
	e := newE2EEnv(t)
	if e.metricsServerAvailable() {
		t.Skip("metrics-server is installed; this test only runs when it is missing")
	}

	res, err := e.manager.handleGetNodeMetrics(context.Background(), makeRequest(map[string]any{
		"context": e.context,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected error when metrics-server unavailable")
	requireContains(t, text, "metrics-server is not available", "expected clear missing-metrics-server error")
}

// When metrics-server IS available, exercise the happy path. Both tests below
// share a common "wait for metrics" helper because the metrics-server scrapes
// usage with some delay.
func waitForPodMetric(t *testing.T, e *e2eEnv, name string) {
	t.Helper()
	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	waitForCondition(t, 90*time.Second, func() bool {
		_, err := cli.MetricsClient.MetricsV1beta1().PodMetricses(e.namespace).Get(context.Background(), name, metav1Get())
		return err == nil
	})
}

func TestE2E_GetPodMetrics_HappyPath(t *testing.T) {
	e := newE2EEnv(t)
	if !e.metricsServerAvailable() {
		t.Skip("metrics-server is not installed in the test cluster")
	}

	name := "kmcp-e2e-podmetrics"
	e.applyManifest(`
apiVersion: v1
kind: Pod
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
spec:
  restartPolicy: Never
  containers:
  - name: main
    image: busybox:1.36
    command: ["sh", "-c", "sleep 3600"]
`)
	e.waitForPodReady(name, 90*time.Second)
	waitForPodMetric(t, e, name)

	res, err := e.manager.handleGetPodMetrics(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"namespace": e.namespace,
		"name":      name,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "get_pod_metrics happy path")
	requireContains(t, out, name, "expected pod name in metrics output")
	requireContains(t, out, "containers:", "expected containers section")
	requireContains(t, out, "usage:", "expected usage section")
}

func TestE2E_GetNodeMetrics_HappyPath(t *testing.T) {
	e := newE2EEnv(t)
	if !e.metricsServerAvailable() {
		t.Skip("metrics-server is not installed in the test cluster")
	}

	res, err := e.manager.handleGetNodeMetrics(context.Background(), makeRequest(map[string]any{
		"context": e.context,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "get_node_metrics happy path")
	requireContains(t, out, "items:", "expected items list")
	requireContains(t, out, "usage:", "expected usage data")
}
