//go:build e2e

/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Integration tests for the k8stools package. They require a real Kubernetes
// cluster (Kind by default) and are gated behind the `integration` build tag
// so `go test ./...` ignores them.
//
//	go test -tags=integration ./internal/k8stools/...
//
// Required environment:
//   - KUBECONFIG (default: ~/.kube/config)
//   - KMCP_E2E_CONTEXT (default: current-context of KUBECONFIG)
//
// Each test runs in its own random namespace which is cleaned up at the end.
package k8stools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kubernetes-mcp/api"
	"kubernetes-mcp/internal/authorization"
	"kubernetes-mcp/internal/kubernetes"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
)

// e2eContextName returns the kubernetes context name used by the integration
// tests. Falls back to the current-context of the default kubeconfig.
func e2eContextName(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("KMCP_E2E_CONTEXT"); v != "" {
		return v
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if v := os.Getenv("KUBECONFIG"); v != "" {
		rules.ExplicitPath = v
	}
	cfg, err := rules.Load()
	if err != nil {
		t.Fatalf("could not load kubeconfig to detect current-context: %v\nSet KMCP_E2E_CONTEXT explicitly", err)
	}
	if cfg.CurrentContext == "" {
		t.Fatalf("kubeconfig has no current-context; set KMCP_E2E_CONTEXT explicitly")
	}
	return cfg.CurrentContext
}

// kubeconfigPath returns the kubeconfig path used for tests.
func kubeconfigPath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	if home, _ := os.UserHomeDir(); home != "" {
		return filepath.Join(home, ".kube", "config")
	}
	return ""
}

// e2eEnv groups everything an integration test needs.
type e2eEnv struct {
	t             *testing.T
	manager       *Manager
	clientManager *kubernetes.ClientManager
	context       string
	namespace     string
}

// newE2EEnv builds an integration-test environment with a unique namespace.
func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()

	ctxName := e2eContextName(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	kCfg := &api.KubernetesConfig{
		DefaultContext: ctxName,
		Contexts: []api.KubernetesContextConfig{
			{
				Name:              ctxName,
				Kubeconfig:        kubeconfigPath(),
				KubeconfigContext: ctxName,
				Description:       "integration test cluster",
			},
		},
	}

	cm, err := kubernetes.NewClientManager(logger, kCfg)
	if err != nil {
		t.Fatalf("failed to create client manager: %v", err)
	}
	t.Cleanup(func() { cm.Stop() })

	authzCfg := &api.AuthorizationConfig{
		AllowAnonymous: true,
		Policies: []api.AuthorizationPolicy{
			{
				Name:  "allow-all",
				Match: api.MatchConfig{Expression: "true"},
				Rules: []api.AuthorizationRule{
					{
						Effect:   api.RuleEffectAllow,
						Tools:    []string{"*"},
						Contexts: []string{"*"},
					},
				},
			},
		},
	}
	authz, err := authorization.NewEvaluator(authzCfg)
	if err != nil {
		t.Fatalf("failed to build authorization evaluator: %v", err)
	}

	mcpServer := server.NewMCPServer("kmcp-e2e", "0.0.0", server.WithToolCapabilities(true))

	mgr := NewManager(ManagerDependencies{
		Logger:        logger,
		Config:        &api.Configuration{Kubernetes: *kCfg},
		ClientManager: cm,
		Authz:         authz,
		McpServer:     mcpServer,
		ToolPrefix:    "",
	})
	// We don't call mgr.RegisterAll(); tests invoke the unexported handle* methods
	// directly because they live in the same package (whitebox tests).

	ns := randomNamespace(t)
	createNamespace(t, cm, ctxName, ns)
	t.Cleanup(func() { deleteNamespace(cm, ctxName, ns) })

	return &e2eEnv{
		t:             t,
		manager:       mgr,
		clientManager: cm,
		context:       ctxName,
		namespace:     ns,
	}
}

// randomNamespace returns a short unique namespace name like "kmcp-e2e-abcd1234".
func randomNamespace(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "kmcp-e2e-" + hex.EncodeToString(b[:])
}

func createNamespace(t *testing.T, cm *kubernetes.ClientManager, ctxName, ns string) {
	t.Helper()
	cli, err := cm.GetClient(ctxName)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	_, err = cli.Clientset.CoreV1().Namespaces().Create(
		context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
}

func deleteNamespace(cm *kubernetes.ClientManager, ctxName, ns string) {
	cli, err := cm.GetClient(ctxName)
	if err != nil {
		return
	}
	_ = cli.Clientset.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
}

// makeRequest builds a CallToolRequest with the provided arguments.
func makeRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// firstText returns the first text content of a tool result and the IsError flag.
func firstText(res *mcp.CallToolResult) (string, bool) {
	if res == nil {
		return "<nil result>", true
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text, res.IsError
		}
	}
	return "", res.IsError
}

// expectOK fails the test if the tool returned an error result.
func expectOK(t *testing.T, res *mcp.CallToolResult, msg string) string {
	t.Helper()
	text, isErr := firstText(res)
	if isErr {
		t.Fatalf("%s: tool returned error: %s", msg, text)
	}
	return text
}

// expectErr fails the test if the tool succeeded; returns the error text.
func expectErr(t *testing.T, res *mcp.CallToolResult, msg string) string {
	t.Helper()
	text, isErr := firstText(res)
	if !isErr {
		t.Fatalf("%s: expected an error but tool succeeded; got: %s", msg, text)
	}
	return text
}

// gvrOf builds a GVR.
func gvrOf(group, version, resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
}

// resourceExists asserts a resource exists. Tries namespaced (in the test
// namespace) first and falls back to cluster-scoped.
func (e *e2eEnv) resourceExists(group, version, resource, name string) bool {
	e.t.Helper()
	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		return false
	}
	gvr := gvrOf(group, version, resource)
	if _, err := cli.DynamicClient.Resource(gvr).Namespace(e.namespace).Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		return true
	}
	if _, err := cli.DynamicClient.Resource(gvr).Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		return true
	}
	return false
}

// avoid unused-import lint when apierrors not referenced elsewhere
var _ = apierrors.IsNotFound

// waitForCondition polls fn every 250ms until it returns true or timeout fires.
func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for condition", timeout)
}

// requireContains fails the test if `text` does not contain `needle`.
func requireContains(t *testing.T, text, needle, msg string) {
	t.Helper()
	if !strings.Contains(text, needle) {
		t.Fatalf("%s: expected text to contain %q\ngot:\n%s", msg, needle, text)
	}
}

// applyManifest is a tiny helper to apply a manifest via the tool, returning the
// reported text. Fails on tool error.
func (e *e2eEnv) applyManifest(manifest string) string {
	e.t.Helper()
	res, err := e.manager.handleApplyManifest(context.Background(), makeRequest(map[string]any{
		"context":  e.context,
		"manifest": manifest,
	}))
	if err != nil {
		e.t.Fatalf("apply: go-error %v", err)
	}
	return expectOK(e.t, res, "apply_manifest")
}

// waitForPodReady polls until the named Pod in the test namespace is Running
// with all containers ready, or fails the test on timeout.
func (e *e2eEnv) waitForPodReady(name string, timeout time.Duration) {
	e.t.Helper()
	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		e.t.Fatalf("get client: %v", err)
	}
	waitForCondition(e.t, timeout, func() bool {
		pod, err := cli.Clientset.CoreV1().Pods(e.namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		if pod.Status.Phase != corev1.PodRunning {
			return false
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				return false
			}
		}
		return true
	})
}

// metricsServerAvailable returns true if the cluster has metrics.k8s.io API
// registered (i.e. metrics-server is installed).
func (e *e2eEnv) metricsServerAvailable() bool {
	e.t.Helper()
	cli, err := e.clientManager.GetClient(e.context)
	if err != nil {
		return false
	}
	groups, err := cli.Clientset.Discovery().ServerGroups()
	if err != nil {
		return false
	}
	for _, g := range groups.Groups {
		if g.Name == "metrics.k8s.io" {
			return true
		}
	}
	return false
}

// metav1Delete returns a default DeleteOptions for use in cleanups.
func metav1Delete() metav1.DeleteOptions { return metav1.DeleteOptions{} }

// metav1Options returns a default ListOptions.
func metav1Options() metav1.ListOptions { return metav1.ListOptions{} }

// metav1Get returns a default GetOptions.
func metav1Get() metav1.GetOptions { return metav1.GetOptions{} }

// metav1Update returns a default UpdateOptions.
func metav1Update() metav1.UpdateOptions { return metav1.UpdateOptions{} }

// nestedMap is a tiny shim around unstructured.NestedMap to avoid importing
// the package across every test file.
func nestedMap(obj map[string]any, fields ...string) (map[string]any, bool, error) {
	return unstructured.NestedMap(obj, fields...)
}
