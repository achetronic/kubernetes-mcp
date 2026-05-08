//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// E2E tests for undo_rollout against Deployments, StatefulSets and DaemonSets.
package k8stools

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// applyDeploymentWithImage creates/updates a 1-replica deployment using the
// given container image. Used to drive multiple revisions in the rollout
// history.
func applyDeploymentWithImage(e *e2eEnv, name, image string) {
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
      - name: c
        image: ` + image + `
`)
}

// waitForDeploymentRevision polls the Deployment until its
// 'deployment.kubernetes.io/revision' annotation equals 'wanted'.
func waitForDeploymentRevision(e *e2eEnv, name, wanted string, timeout time.Duration) {
	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		e.t.Fatalf("get client: %v", err)
	}
	gvr := gvrOf("apps", "v1", "deployments")
	waitForCondition(e.t, timeout, func() bool {
		dep, err := cli.DynamicClient.Resource(gvr).Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		annotations, _, _ := unstructured.NestedMap(dep.Object, "metadata", "annotations")
		got, _ := annotations["deployment.kubernetes.io/revision"].(string)
		return got == wanted
	})
}

// deploymentImage returns the first container image of the Deployment's pod template.
func deploymentImage(e *e2eEnv, name string) string {
	cli, _ := e.clientManager.GetClient(e.context)
	dep, err := cli.DynamicClient.Resource(gvrOf("apps", "v1", "deployments")).
		Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		e.t.Fatalf("get deployment: %v", err)
	}
	containers, _, _ := unstructured.NestedSlice(dep.Object, "spec", "template", "spec", "containers")
	if len(containers) == 0 {
		return ""
	}
	c, _ := containers[0].(map[string]any)
	img, _ := c["image"].(string)
	return img
}

func TestE2E_UndoRollout_Deployment_DefaultGoesToPrevious(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-dep"

	// Revision 1: nginx:1.25
	applyDeploymentWithImage(e, name, "nginx:1.25")
	waitForDeploymentRevision(e, name, "1", 60*time.Second)

	// Revision 2: nginx:1.27 (creates a new ReplicaSet)
	applyDeploymentWithImage(e, name, "nginx:1.27")
	waitForDeploymentRevision(e, name, "2", 60*time.Second)

	if got := deploymentImage(e, name); got != "nginx:1.27" {
		t.Fatalf("preconditions failed: expected nginx:1.27, got %q", got)
	}

	// Undo without to_revision → should go to revision 1 (i.e. nginx:1.25)
	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "deployments",
		"name":      name,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "undo_rollout default")
	requireContains(t, out, "to revision 1", "expected revision 1 in success message")

	// The Deployment itself reports the new image; the revision counter bumps.
	waitForCondition(t, 30*time.Second, func() bool {
		return deploymentImage(e, name) == "nginx:1.25"
	})
}

func TestE2E_UndoRollout_Deployment_ToSpecificRevision(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-dep-to"

	applyDeploymentWithImage(e, name, "nginx:1.25") // r1
	waitForDeploymentRevision(e, name, "1", 60*time.Second)

	applyDeploymentWithImage(e, name, "nginx:1.27") // r2
	waitForDeploymentRevision(e, name, "2", 60*time.Second)

	applyDeploymentWithImage(e, name, "nginx:1.29") // r3
	waitForDeploymentRevision(e, name, "3", 60*time.Second)

	// Roll back specifically to revision 1.
	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":     e.context,
		"group":       "apps",
		"version":     "v1",
		"resource":    "deployments",
		"name":        name,
		"namespace":   e.namespace,
		"to_revision": float64(1),
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "undo_rollout to_revision=1")
	requireContains(t, out, "to revision 1", "expected revision 1 in success message")

	waitForCondition(t, 30*time.Second, func() bool {
		return deploymentImage(e, name) == "nginx:1.25"
	})
}

func TestE2E_UndoRollout_Deployment_ToCurrentRevisionFails(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-dep-current"
	applyDeploymentWithImage(e, name, "nginx:1.25") // r1
	waitForDeploymentRevision(e, name, "1", 60*time.Second)
	applyDeploymentWithImage(e, name, "nginx:1.27") // r2
	waitForDeploymentRevision(e, name, "2", 60*time.Second)

	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":     e.context,
		"group":       "apps",
		"version":     "v1",
		"resource":    "deployments",
		"name":        name,
		"namespace":   e.namespace,
		"to_revision": float64(2),
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "undo to current revision should fail")
	requireContains(t, text, "already the current one", "expected current-revision error")
}

func TestE2E_UndoRollout_Deployment_NoHistoryFails(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-dep-nohistory"
	applyDeploymentWithImage(e, name, "nginx:1.25")
	waitForDeploymentRevision(e, name, "1", 60*time.Second)

	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "deployments",
		"name":      name,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "undo with only one revision should fail")
	requireContains(t, text, "no previous revision", "expected no-previous-revision error")
}

// applyStatefulSetWithImage creates/updates a 1-replica headless StatefulSet
// with a configurable image to generate ControllerRevisions.
func applyStatefulSetWithImage(e *e2eEnv, name, image string) {
	// Headless service to satisfy the StatefulSet requirement.
	e.applyManifest(`
apiVersion: v1
kind: Service
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
spec:
  clusterIP: None
  selector:
    app: ` + name + `
  ports:
  - port: 80
`)
	e.applyManifest(`
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
spec:
  serviceName: ` + name + `
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
      - name: c
        image: ` + image + `
`)
}

// waitForControllerRevisionCount waits until the parent (named) workload has at
// least 'wanted' ControllerRevisions in the namespace.
func waitForControllerRevisionCount(e *e2eEnv, parentName string, wanted int, timeout time.Duration) {
	cli, _ := e.clientManager.GetClient(e.context)
	crGVR := gvrOf("apps", "v1", "controllerrevisions")
	waitForCondition(e.t, timeout, func() bool {
		list, err := cli.DynamicClient.Resource(crGVR).Namespace(e.namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return false
		}
		count := 0
		for _, item := range list.Items {
			refs, _, _ := unstructured.NestedSlice(item.Object, "metadata", "ownerReferences")
			for _, r := range refs {
				ref, _ := r.(map[string]any)
				if ref == nil {
					continue
				}
				if name, _ := ref["name"].(string); name == parentName {
					count++
					break
				}
			}
		}
		return count >= wanted
	})
}

// statefulSetImage returns the first container image of the StatefulSet template.
func statefulSetImage(e *e2eEnv, name string) string {
	cli, _ := e.clientManager.GetClient(e.context)
	ss, err := cli.DynamicClient.Resource(gvrOf("apps", "v1", "statefulsets")).
		Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		e.t.Fatalf("get statefulset: %v", err)
	}
	containers, _, _ := unstructured.NestedSlice(ss.Object, "spec", "template", "spec", "containers")
	if len(containers) == 0 {
		return ""
	}
	c, _ := containers[0].(map[string]any)
	img, _ := c["image"].(string)
	return img
}

func TestE2E_UndoRollout_StatefulSet_DefaultGoesToPrevious(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-ss"

	applyStatefulSetWithImage(e, name, "nginx:1.25")
	waitForControllerRevisionCount(e, name, 1, 60*time.Second)

	applyStatefulSetWithImage(e, name, "nginx:1.27")
	waitForControllerRevisionCount(e, name, 2, 60*time.Second)

	if got := statefulSetImage(e, name); got != "nginx:1.27" {
		t.Fatalf("preconditions failed: expected nginx:1.27, got %q", got)
	}

	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "statefulsets",
		"name":      name,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "undo_rollout statefulset default")
	if !strings.Contains(out, "Successfully rolled back statefulsets") {
		t.Fatalf("expected success message, got: %s", out)
	}

	waitForCondition(t, 30*time.Second, func() bool {
		return statefulSetImage(e, name) == "nginx:1.25"
	})
}

func TestE2E_UndoRollout_StatefulSet_NoHistoryFails(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-ss-nohistory"
	applyStatefulSetWithImage(e, name, "nginx:1.25")
	waitForControllerRevisionCount(e, name, 1, 60*time.Second)

	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "statefulsets",
		"name":      name,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "undo with single revision should fail")
	requireContains(t, text, "no previous revision", "expected no-previous-revision error")
}

// applyDaemonSetWithImage creates/updates a DaemonSet to generate
// ControllerRevisions.
func applyDaemonSetWithImage(e *e2eEnv, name, image string) {
	e.applyManifest(`
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
spec:
  selector:
    matchLabels:
      app: ` + name + `
  template:
    metadata:
      labels:
        app: ` + name + `
    spec:
      tolerations:
      - operator: Exists
      containers:
      - name: c
        image: ` + image + `
`)
}

// daemonSetImage returns the first container image of the DaemonSet template.
func daemonSetImage(e *e2eEnv, name string) string {
	cli, _ := e.clientManager.GetClient(e.context)
	ds, err := cli.DynamicClient.Resource(gvrOf("apps", "v1", "daemonsets")).
		Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		e.t.Fatalf("get daemonset: %v", err)
	}
	containers, _, _ := unstructured.NestedSlice(ds.Object, "spec", "template", "spec", "containers")
	if len(containers) == 0 {
		return ""
	}
	c, _ := containers[0].(map[string]any)
	img, _ := c["image"].(string)
	return img
}

func TestE2E_UndoRollout_DaemonSet_DefaultGoesToPrevious(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-ds"

	applyDaemonSetWithImage(e, name, "nginx:1.25")
	waitForControllerRevisionCount(e, name, 1, 60*time.Second)

	applyDaemonSetWithImage(e, name, "nginx:1.27")
	waitForControllerRevisionCount(e, name, 2, 60*time.Second)

	if got := daemonSetImage(e, name); got != "nginx:1.27" {
		t.Fatalf("preconditions failed: expected nginx:1.27, got %q", got)
	}

	res, err := e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"group":     "apps",
		"version":   "v1",
		"resource":  "daemonsets",
		"name":      name,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "undo_rollout daemonset default")
	if !strings.Contains(out, "Successfully rolled back daemonsets") {
		t.Fatalf("expected success message, got: %s", out)
	}

	waitForCondition(t, 30*time.Second, func() bool {
		return daemonSetImage(e, name) == "nginx:1.25"
	})
}
