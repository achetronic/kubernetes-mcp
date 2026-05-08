//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// E2E tests for log retrieval and command execution against running Pods.
package k8stools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestE2E_GetLogs_ReturnsStdout(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-logs"
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
    command: ["sh", "-c", "echo hello-from-stdout && sleep 3600"]
`)
	e.waitForPodReady(name, 90*time.Second)

	res, err := e.manager.handleGetLogs(context.Background(), makeRequest(map[string]any{
		"context":    e.context,
		"name":       name,
		"namespace":  e.namespace,
		"tail_lines": float64(20),
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "get_logs")
	requireContains(t, out, "hello-from-stdout", "expected stdout in logs")
}

func TestE2E_GetLogs_TailLines(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-logs-tail"
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
    command: ["sh", "-c", "for i in $(seq 1 10); do echo line$i; done; sleep 3600"]
`)
	e.waitForPodReady(name, 90*time.Second)

	res, err := e.manager.handleGetLogs(context.Background(), makeRequest(map[string]any{
		"context":    e.context,
		"name":       name,
		"namespace":  e.namespace,
		"tail_lines": float64(3),
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "get_logs tail")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}
	requireContains(t, out, "line8", "expected line8 in tail")
	requireContains(t, out, "line10", "expected line10 in tail")
}

func TestE2E_GetLogs_NotFound(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleGetLogs(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"name":      "this-pod-does-not-exist",
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected error for missing pod")
	requireContains(t, text, "not found", "expected NotFound error")
}

func TestE2E_ExecCommand_Stdout(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-exec"
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

	res, err := e.manager.handleExecCommand(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"name":      name,
		"namespace": e.namespace,
		"command":   []any{"echo", "ping-pong"},
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "exec_command")
	requireContains(t, out, "ping-pong", "expected exec output")
}

func TestE2E_ExecCommand_RequiresCommand(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleExecCommand(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"name":      "irrelevant",
		"namespace": e.namespace,
		"command":   []any{},
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "expected validation error for empty command")
	requireContains(t, text, "command is required", "expected required-command message")
}
