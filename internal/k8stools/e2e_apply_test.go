//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// Integration tests for apply_manifest and diff_manifest, with emphasis on
// RESTMapper-based Kind -> GVR resolution (especially irregular plurals
// that the previous heuristic mishandled).
package k8stools

import (
	"context"
	"testing"
)

func TestE2E_ApplyManifest_ConfigMap(t *testing.T) {
	e := newE2EEnv(t)

	manifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-cm
  namespace: ` + e.namespace + `
data:
  k: v
`
	out := e.applyManifest(manifest)
	requireContains(t, out, "ConfigMap/kmcp-e2e-cm", "expected success message")

	if !e.resourceExists("", "v1", "configmaps", "kmcp-e2e-cm") {
		t.Fatalf("ConfigMap was not created")
	}
}

func TestE2E_ApplyManifest_Deployment(t *testing.T) {
	e := newE2EEnv(t)

	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kmcp-e2e-deploy
  namespace: ` + e.namespace + `
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kmcp-e2e
  template:
    metadata:
      labels:
        app: kmcp-e2e
    spec:
      containers:
      - name: nginx
        image: nginx:alpine
`
	out := e.applyManifest(manifest)
	requireContains(t, out, "Deployment/kmcp-e2e-deploy", "expected success message")

	if !e.resourceExists("apps", "v1", "deployments", "kmcp-e2e-deploy") {
		t.Fatalf("Deployment was not created")
	}
}

// Regression test: the previous heuristic (kind+s) would have produced
// "storageclasss" (with three s's) and the API call would have 404'd. With
// the RESTMapper this resolves correctly to "storageclasses".
func TestE2E_ApplyManifest_StorageClass_IrregularPlural(t *testing.T) {
	e := newE2EEnv(t)

	manifest := `
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: kmcp-e2e-sc
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: Immediate
`
	out := e.applyManifest(manifest)
	requireContains(t, out, "StorageClass/kmcp-e2e-sc", "expected success message")

	// Cleanup the cluster-scoped resource explicitly.
	t.Cleanup(func() {
		cli, err := e.clientManager.GetClient(e.context)
		if err != nil {
			return
		}
		_ = cli.DynamicClient.Resource(gvrOf("storage.k8s.io", "v1", "storageclasses")).
			Delete(context.Background(), "kmcp-e2e-sc", metav1Delete())
	})

	if !e.resourceExists("storage.k8s.io", "v1", "storageclasses", "kmcp-e2e-sc") {
		t.Fatalf("StorageClass was not created (irregular plural regression)")
	}
}

// Another irregular plural: networkpolicies.
func TestE2E_ApplyManifest_NetworkPolicy_IrregularPlural(t *testing.T) {
	e := newE2EEnv(t)

	manifest := `
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kmcp-e2e-np
  namespace: ` + e.namespace + `
spec:
  podSelector: {}
  policyTypes: [Ingress]
`
	out := e.applyManifest(manifest)
	requireContains(t, out, "NetworkPolicy/kmcp-e2e-np", "expected success message")

	if !e.resourceExists("networking.k8s.io", "v1", "networkpolicies", "kmcp-e2e-np") {
		t.Fatalf("NetworkPolicy was not created")
	}
}

func TestE2E_ApplyManifest_BogusKind_ReturnsRESTMapperError(t *testing.T) {
	e := newE2EEnv(t)

	res, err := e.manager.handleApplyManifest(context.Background(), makeRequest(map[string]any{
		"context": e.context,
		"manifest": `
apiVersion: v1
kind: ThisKindDoesNotExist
metadata:
  name: x
  namespace: ` + e.namespace + `
`,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	text := expectErr(t, res, "apply_manifest with bogus Kind should fail")
	requireContains(t, text, "no matches for kind", "RESTMapper should report unknown Kind")
}

func TestE2E_ApplyManifest_NamespaceOverride(t *testing.T) {
	e := newE2EEnv(t)

	// Manifest declares no namespace; override should set it.
	manifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-override
data:
  k: v
`
	res, err := e.manager.handleApplyManifest(context.Background(), makeRequest(map[string]any{
		"context":   e.context,
		"manifest":  manifest,
		"namespace": e.namespace,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	expectOK(t, res, "apply with namespace override")

	if !e.resourceExists("", "v1", "configmaps", "kmcp-e2e-override") {
		t.Fatalf("ConfigMap was not created in overridden namespace")
	}
}

func TestE2E_DiffManifest_NoChanges(t *testing.T) {
	e := newE2EEnv(t)

	manifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-diff
  namespace: ` + e.namespace + `
data:
  k: v
`
	e.applyManifest(manifest)

	res, err := e.manager.handleDiffManifest(context.Background(), makeRequest(map[string]any{
		"context":  e.context,
		"manifest": manifest,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "diff_manifest no changes")
	requireContains(t, out, "No changes detected", "expected no-changes message")
}

func TestE2E_DiffManifest_Changes(t *testing.T) {
	e := newE2EEnv(t)

	original := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-diff2
  namespace: ` + e.namespace + `
data:
  k: original
`
	e.applyManifest(original)

	modified := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-diff2
  namespace: ` + e.namespace + `
data:
  k: changed
  newkey: x
`
	res, err := e.manager.handleDiffManifest(context.Background(), makeRequest(map[string]any{
		"context":  e.context,
		"manifest": modified,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "diff_manifest with changes")
	requireContains(t, out, "data.k", "expected diff to mention changed key")
	requireContains(t, out, "data.newkey", "expected diff to mention new key")
}

func TestE2E_DiffManifest_NotExisting(t *testing.T) {
	e := newE2EEnv(t)

	manifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kmcp-e2e-doesnotexist
  namespace: ` + e.namespace + `
data:
  k: v
`
	res, err := e.manager.handleDiffManifest(context.Background(), makeRequest(map[string]any{
		"context":  e.context,
		"manifest": manifest,
	}))
	if err != nil {
		t.Fatalf("go-error: %v", err)
	}
	out := expectOK(t, res, "diff_manifest of non-existing")
	requireContains(t, out, "would CREATE a new resource", "expected creation hint")
}
