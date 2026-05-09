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
	"k8s.io/apimachinery/pkg/runtime/schema"
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

// waitForControllerRevisionObserved waits until the parent's
// status.currentRevision points at a ControllerRevision whose 'revision'
// field equals 'wantedRevision'. This is the right signal to wait for before
// driving 'undo_rollout': the count of revisions can grow before the
// controller has actually rolled out the latest one.
func waitForControllerRevisionObserved(e *e2eEnv, parentGVR schema.GroupVersionResource, parentName string, wantedRevision int64, timeout time.Duration) {
	cli, _ := e.clientManager.GetClient(e.context)
	crGVR := gvrOf("apps", "v1", "controllerrevisions")
	waitForCondition(e.t, timeout, func() bool {
		parent, err := cli.DynamicClient.Resource(parentGVR).Namespace(e.namespace).Get(context.Background(), parentName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		currentRevName, _, _ := unstructured.NestedString(parent.Object, "status", "currentRevision")
		if currentRevName == "" {
			return false
		}
		cr, err := cli.DynamicClient.Resource(crGVR).Namespace(e.namespace).Get(context.Background(), currentRevName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		rev, _, _ := unstructured.NestedInt64(cr.Object, "revision")
		return rev == wantedRevision
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
	ssGVR := gvrOf("apps", "v1", "statefulsets")

	applyStatefulSetWithImage(e, name, "nginx:1.25")
	waitForControllerRevisionCount(e, name, 1, 60*time.Second)
	waitForControllerRevisionObserved(e, ssGVR, name, 1, 60*time.Second)

	applyStatefulSetWithImage(e, name, "nginx:1.27")
	waitForControllerRevisionCount(e, name, 2, 60*time.Second)
	waitForControllerRevisionObserved(e, ssGVR, name, 2, 60*time.Second)

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

// --- Regression tests for the bugs caught by the post-implementation audit ---

// R1: structural template changes between revisions must round-trip.
//
// kubectl rollout undo replaces /spec/template wholesale (JSON Patch). A
// strategic-merge patch would have left the sidecar container in place.
func TestE2E_UndoRollout_Deployment_RemovesContainerAddedInLaterRevision(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-dep-shape"

	// Revision 1: a single container 'main'.
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
      - name: main
        image: nginx:1.25
`)
	waitForDeploymentRevision(e, name, "1", 60*time.Second)

	// Revision 2: same 'main' container PLUS a 'sidecar' that did not exist
	// in r1. After the rollback to r1 the sidecar must be gone.
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
      - name: main
        image: nginx:1.25
      - name: sidecar
        image: busybox:1.36
        command: ["sh","-c","sleep 3600"]
`)
	waitForDeploymentRevision(e, name, "2", 60*time.Second)

	// Confirm the sidecar made it into the live spec.
	cli, _ := e.clientManager.GetClient(e.context)
	dep, err := cli.DynamicClient.Resource(gvrOf("apps", "v1", "deployments")).
		Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	containers, _, _ := unstructured.NestedSlice(dep.Object, "spec", "template", "spec", "containers")
	if len(containers) != 2 {
		t.Fatalf("preconditions failed: expected 2 containers, got %d", len(containers))
	}

	// Roll back to revision 1.
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

	// The sidecar must be gone.
	waitForCondition(t, 30*time.Second, func() bool {
		dep, err := cli.DynamicClient.Resource(gvrOf("apps", "v1", "deployments")).
			Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		containers, _, _ := unstructured.NestedSlice(dep.Object, "spec", "template", "spec", "containers")
		return len(containers) == 1
	})

	// And confirm the sole container is 'main' with the original image, not
	// 'sidecar' or some merged hybrid.
	dep, _ = cli.DynamicClient.Resource(gvrOf("apps", "v1", "deployments")).
		Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
	containers, _, _ = unstructured.NestedSlice(dep.Object, "spec", "template", "spec", "containers")
	c, _ := containers[0].(map[string]any)
	if name, _ := c["name"].(string); name != "main" {
		t.Fatalf("expected container 'main' after rollback, got %q", name)
	}
	if img, _ := c["image"].(string); img != "nginx:1.25" {
		t.Fatalf("expected image 'nginx:1.25' after rollback, got %q", img)
	}
}

// R1 (annotations): rollback restores deployment-level annotations from the
// target ReplicaSet (minus the controller-managed ones), instead of leaving
// whatever the current Deployment had.
func TestE2E_UndoRollout_Deployment_RestoresAnnotationsFromTargetRS(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-dep-annotations"
	cli, _ := e.clientManager.GetClient(e.context)

	// Revision 1 carries an annotation 'kmcp.test/r1: yes'.
	e.applyManifest(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
  annotations:
    kmcp.test/r1: "yes"
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
      - name: main
        image: nginx:1.25
`)
	waitForDeploymentRevision(e, name, "1", 60*time.Second)

	// Revision 2 swaps the annotation entirely.
	e.applyManifest(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ` + name + `
  namespace: ` + e.namespace + `
  annotations:
    kmcp.test/r2: "yes"
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
      - name: main
        image: nginx:1.27
`)
	waitForDeploymentRevision(e, name, "2", 60*time.Second)

	// Roll back to revision 1.
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
	expectOK(t, res, "undo_rollout default")

	// The deployment annotations must reflect the r1 RS, not the r2 state.
	waitForCondition(t, 30*time.Second, func() bool {
		dep, err := cli.DynamicClient.Resource(gvrOf("apps", "v1", "deployments")).
			Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		annotations, _, _ := unstructured.NestedStringMap(dep.Object, "metadata", "annotations")
		_, hasR1 := annotations["kmcp.test/r1"]
		_, hasR2 := annotations["kmcp.test/r2"]
		return hasR1 && !hasR2
	})
}

// R2: after several undos, the next undo without to_revision must roll back
// to the revision immediately before status.currentRevision, NOT to the one
// before the highest revision in the history.
func TestE2E_UndoRollout_StatefulSet_HonoursStatusCurrentRevision(t *testing.T) {
	e := newE2EEnv(t)

	name := "kmcp-e2e-undo-ss-current"
	ssGVR := gvrOf("apps", "v1", "statefulsets")

	applyStatefulSetWithImage(e, name, "nginx:1.25") // r1
	waitForControllerRevisionCount(e, name, 1, 60*time.Second)
	waitForControllerRevisionObserved(e, ssGVR, name, 1, 60*time.Second)

	applyStatefulSetWithImage(e, name, "nginx:1.27") // r2
	waitForControllerRevisionCount(e, name, 2, 60*time.Second)
	waitForControllerRevisionObserved(e, ssGVR, name, 2, 60*time.Second)

	applyStatefulSetWithImage(e, name, "nginx:1.29") // r3
	waitForControllerRevisionCount(e, name, 3, 60*time.Second)
	waitForControllerRevisionObserved(e, ssGVR, name, 3, 60*time.Second)

	// Undo from r3 -> r2.
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
	expectOK(t, res, "undo_rollout statefulset (1st)")
	waitForCondition(t, 30*time.Second, func() bool {
		return statefulSetImage(e, name) == "nginx:1.27"
	})

	// Wait for the controller to mark the rollback as the current revision.
	// The SS controller will create a new ControllerRevision (call it r4)
	// whose template equals r2's, and flip status.currentRevision to that.
	waitForCondition(t, 60*time.Second, func() bool {
		ss, err := e.clientManager.GetClient(e.context)
		if err != nil {
			return false
		}
		obj, err := ss.DynamicClient.Resource(ssGVR).Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		updRev, _, _ := unstructured.NestedString(obj.Object, "status", "updateRevision")
		curRev, _, _ := unstructured.NestedString(obj.Object, "status", "currentRevision")
		return updRev != "" && curRev == updRev
	})

	// Now another default undo MUST go from current (r4 — image=1.27) to the
	// next-lower revision in history, which is r3 (image=1.29). With the old
	// "highest is current" assumption we would have skipped r4 entirely and
	// gone r3 -> r2, ending up at 1.27 (a no-op visible as 1.27 -> 1.27).
	res, err = e.manager.handleUndoRollout(context.Background(), makeRequest(map[string]any{
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
	expectOK(t, res, "undo_rollout statefulset (2nd)")
	waitForCondition(t, 60*time.Second, func() bool {
		return statefulSetImage(e, name) == "nginx:1.29"
	})
}
