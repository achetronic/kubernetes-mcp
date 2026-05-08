//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// E2E tests for list_events.
package k8stools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// triggerWarningEvent creates a Pod that fails to pull its image, generating
// at least one Warning event in the test namespace.
func triggerWarningEvent(e *e2eEnv, name string) {
	e.applyManifest(`
apiVersion: v1
kind: Pod
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
spec:
  restartPolicy: Never
  containers:
  - name: x
    image: this-image-does-not-exist:nope
`)
}

func TestE2E_ListEvents_NamespaceScope(t *testing.T) {
	e := newE2EEnv(t)
	triggerWarningEvent(e, "kmcp-e2e-evt")

	cli, _ := e.clientManager.GetClient(e.context)
	waitForCondition(t, 60*time.Second, func() bool {
		evs, err := cli.Clientset.CoreV1().Events(e.namespace).List(context.Background(), metav1Options())
		if err != nil {
			return false
		}
		for _, ev := range evs.Items {
			if ev.InvolvedObject.Name == "kmcp-e2e-evt" {
				return true
			}
		}
		return false
	})

	res, err := e.manager.handleListEvents(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_events")
	requireContains(t, out, "kmcp-e2e-evt", "expected reference to triggering pod")
}

func TestE2E_ListEvents_FilterByType(t *testing.T) {
	e := newE2EEnv(t)
	triggerWarningEvent(e, "kmcp-e2e-evt-warn")

	cli, _ := e.clientManager.GetClient(e.context)
	waitForCondition(t, 60*time.Second, func() bool {
		evs, err := cli.Clientset.CoreV1().Events(e.namespace).List(context.Background(), metav1Options())
		if err != nil {
			return false
		}
		for _, ev := range evs.Items {
			if ev.Type == "Warning" {
				return true
			}
		}
		return false
	})

	res, err := e.manager.handleListEvents(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"namespace": e.namespace,
		"types":     []any{"Warning"},
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_events filtered")
	if strings.Contains(out, "type: Normal") {
		t.Fatalf("expected only Warning events; saw a Normal event:\n%s", out)
	}
	requireContains(t, out, "type: Warning", "expected at least one Warning event")
}

func TestE2E_ListEvents_FieldSelector(t *testing.T) {
	e := newE2EEnv(t)
	triggerWarningEvent(e, "kmcp-e2e-evt-target")
	// And another one whose events we want to exclude.
	triggerWarningEvent(e, "kmcp-e2e-evt-other")

	cli, _ := e.clientManager.GetClient(e.context)
	waitForCondition(t, 60*time.Second, func() bool {
		evs, _ := cli.Clientset.CoreV1().Events(e.namespace).List(context.Background(), metav1Options())
		var seenTarget, seenOther bool
		for _, ev := range evs.Items {
			if ev.InvolvedObject.Name == "kmcp-e2e-evt-target" {
				seenTarget = true
			}
			if ev.InvolvedObject.Name == "kmcp-e2e-evt-other" {
				seenOther = true
			}
		}
		return seenTarget && seenOther
	})

	res, err := e.manager.handleListEvents(context.Background(), makeRequest(map[string]any{
		"context":        e.context,
		"namespace":      e.namespace,
		"field_selector": "involvedObject.name=kmcp-e2e-evt-target",
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "list_events with field_selector")
	requireContains(t, out, "kmcp-e2e-evt-target", "expected target pod events")
	if strings.Contains(out, "kmcp-e2e-evt-other") {
		t.Fatalf("expected other pod events to be filtered out; got:\n%s", out)
	}
}
