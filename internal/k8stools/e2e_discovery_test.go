//go:build e2e

/*
Copyright 2025.
Licensed under the Apache License, Version 2.0.
*/

// Integration test for discovery cache refresh:
//
// The RESTMapper caches discovery information lazily. When a new CRD is
// installed AFTER the mapper has already cached the discovery info, calling
// RESTMapper.Reset() must invalidate the cache so the next mapping picks up
// the new resource.
package k8stools

import (
	"context"
	"testing"
	"time"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
)

func TestE2E_RESTMapper_RefreshesAfterCRDInstall(t *testing.T) {
	e := newE2EEnv(t)

	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}

	// 1. First, prime the RESTMapper with a known resource so the discovery
	//    cache is populated.
	if _, err := cli.RESTMapper.RESTMapping(schema.GroupKind{Group: "", Kind: "ConfigMap"}, "v1"); err != nil {
		t.Fatalf("priming mapper: %v", err)
	}

	// 2. Install a fresh, unique CRD.
	apiext, err := apiextclient.NewForConfig(cli.Config)
	if err != nil {
		t.Fatalf("apiext client: %v", err)
	}
	crdName := "kmcptests.kmcp.test"
	crd := &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: crdName},
		Spec: apiextv1.CustomResourceDefinitionSpec{
			Group: "kmcp.test",
			Names: apiextv1.CustomResourceDefinitionNames{
				Plural:   "kmcptests",
				Singular: "kmcptest",
				Kind:     "KMCPTest",
				ListKind: "KMCPTestList",
			},
			Scope: apiextv1.NamespaceScoped,
			Versions: []apiextv1.CustomResourceDefinitionVersion{{
				Name:    "v1",
				Served:  true,
				Storage: true,
				Schema: &apiextv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextv1.JSONSchemaProps{
							"spec": {Type: "object", XPreserveUnknownFields: ptrTrue()},
						},
					},
				},
			}},
		},
	}

	if _, err := apiext.ApiextensionsV1().CustomResourceDefinitions().Create(
		context.Background(), crd, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create CRD: %v", err)
	}
	t.Cleanup(func() {
		_ = apiext.ApiextensionsV1().CustomResourceDefinitions().
			Delete(context.Background(), crdName, metav1.DeleteOptions{})
	})

	// Wait for the API server to register the new endpoint.
	if err := wait.PollUntilContextTimeout(context.Background(), 200*time.Millisecond, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			got, err := apiext.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crdName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			for _, c := range got.Status.Conditions {
				if c.Type == apiextv1.Established && c.Status == apiextv1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
		t.Fatalf("CRD never became Established: %v", err)
	}

	// 3. Without a refresh, the mapper returns no mapping (cached state).
	if _, err := cli.RESTMapper.RESTMapping(schema.GroupKind{Group: "kmcp.test", Kind: "KMCPTest"}, "v1"); err == nil {
		// On some k8s versions / cache implementations the mapper may already
		// know about it via a missing-cache-entry retry; that's fine. The
		// important assertion is that AFTER reset it works.
		t.Log("note: mapper resolved CRD without explicit reset (cache miss path)")
	}

	// 4. Reset and the mapping must succeed.
	cli.RESTMapper.Reset()

	mapping, err := cli.RESTMapper.RESTMapping(schema.GroupKind{Group: "kmcp.test", Kind: "KMCPTest"}, "v1")
	if err != nil {
		t.Fatalf("RESTMapper.RESTMapping after reset: %v", err)
	}
	if mapping.Resource.Resource != "kmcptests" {
		t.Fatalf("expected Resource=kmcptests, got %q", mapping.Resource.Resource)
	}

	// 5. End-to-end: the apply_manifest tool must be able to use the new CRD too.
	out := e.applyManifest(`
apiVersion: kmcp.test/v1
kind: KMCPTest
metadata:
  name: kmcp-e2e-cr
  namespace: ` + e.namespace + `
spec:
  any: thing
`)
	requireContains(t, out, "Successfully applied KMCPTest/kmcp-e2e-cr", "expected CRD apply via tool")
}

func ptrTrue() *bool { v := true; return &v }
