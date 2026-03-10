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

package authorization

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func getKubeContext(t *testing.T) string {
	t.Helper()
	ctx := os.Getenv("KUBE_CONTEXT")
	if ctx == "" {
		t.Skip("set KUBE_CONTEXT to run integration tests (e.g. KUBE_CONTEXT=kind-pollaconruedas)")
	}
	return ctx
}

type clusterClient struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
}

func connectToCluster(t *testing.T, kubeCtx string) *clusterClient {
	t.Helper()

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home := os.Getenv("HOME")
		if home == "" {
			t.Skip("neither KUBECONFIG nor HOME set")
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		t.Skipf("kubeconfig not found at %s", kubeconfig)
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: kubeCtx},
	).ClientConfig()
	if err != nil {
		t.Skipf("cannot build config for context %s: %v", kubeCtx, err)
	}

	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Skipf("cannot create clientset: %v", err)
	}

	dc, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Skipf("cannot create dynamic client: %v", err)
	}

	_, err = cs.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{Limit: 1})
	if err != nil {
		t.Skipf("cluster %s not reachable: %v", kubeCtx, err)
	}

	return &clusterClient{clientset: cs, dynamicClient: dc}
}

type discoveredResource struct {
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
}

func (d discoveredResource) gvr() string {
	if d.Group == "" {
		return fmt.Sprintf("v1/%s", d.Resource)
	}
	return fmt.Sprintf("%s/%s/%s", d.Group, d.Version, d.Resource)
}

func (d discoveredResource) fqn() string {
	if d.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", d.gvr(), d.Namespace, d.Name)
	}
	return fmt.Sprintf("%s/%s", d.gvr(), d.Name)
}

func discoverResources(t *testing.T, cc *clusterClient) []discoveredResource {
	t.Helper()
	ctx := context.Background()

	var result []discoveredResource

	nsList, err := cc.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}
	for _, ns := range nsList.Items {
		result = append(result, discoveredResource{
			Group: "", Version: "v1", Resource: "namespaces", Name: ns.Name,
		})
	}

	namespaces := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	type apiResource struct {
		group      string
		version    string
		resource   string
		namespaced bool
		verbs      []string
	}

	_, apiResourceLists, err := cc.clientset.Discovery().ServerGroupsAndResources()
	if err != nil {
		if !discovery.IsGroupDiscoveryFailedError(err) {
			t.Fatalf("discover resources: %v", err)
		}
	}

	var resources []apiResource
	for _, list := range apiResourceLists {
		gv := list.GroupVersion
		group := ""
		version := gv
		if idx := strings.LastIndex(gv, "/"); idx >= 0 {
			group = gv[:idx]
			version = gv[idx+1:]
		}

		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}

			hasGet := false
			hasList := false
			for _, v := range r.Verbs {
				if v == "get" {
					hasGet = true
				}
				if v == "list" {
					hasList = true
				}
			}
			if !hasGet && !hasList {
				continue
			}

			resources = append(resources, apiResource{
				group:      group,
				version:    version,
				resource:   r.Name,
				namespaced: r.Namespaced,
				verbs:      r.Verbs,
			})
		}
	}

	sampledTypes := map[string]bool{
		"pods": true, "services": true, "configmaps": true, "secrets": true,
		"serviceaccounts": true, "endpoints": true, "events": true,
		"deployments": true, "statefulsets": true, "daemonsets": true, "replicasets": true,
		"jobs": true, "cronjobs": true,
		"ingresses": true, "networkpolicies": true,
		"nodes": true, "persistentvolumes": true, "persistentvolumeclaims": true,
		"clusterroles": true, "clusterrolebindings": true, "roles": true, "rolebindings": true,
	}

	for _, r := range resources {
		if !sampledTypes[r.resource] {
			continue
		}

		if r.namespaced {
			for _, ns := range namespaces {
				items, err := cc.dynamicClient.Resource(schema.GroupVersionResource{
					Group: r.group, Version: r.version, Resource: r.resource,
				}).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 5})
				if err != nil {
					continue
				}
				for _, item := range items.Items {
					result = append(result, discoveredResource{
						Group: r.group, Version: r.version, Resource: r.resource,
						Namespace: ns, Name: item.GetName(),
					})
				}
			}
		} else {
			items, err := cc.dynamicClient.Resource(schema.GroupVersionResource{
				Group: r.group, Version: r.version, Resource: r.resource,
			}).List(ctx, metav1.ListOptions{Limit: 10})
			if err != nil {
				continue
			}
			for _, item := range items.Items {
				result = append(result, discoveredResource{
					Group: r.group, Version: r.version, Resource: r.resource,
					Name: item.GetName(),
				})
			}
		}
	}

	return result
}

func isDeleteAllowedNamespace(ns string) bool {
	deleteNSPatterns := []string{"aplicacion-*", "default"}
	for _, pattern := range deleteNSPatterns {
		if globMatch(pattern, ns) {
			return true
		}
	}
	return false
}

func TestIntegration_SafeOpsAgainstRealCluster(t *testing.T) {
	kubeCtx := getKubeContext(t)
	cc := connectToCluster(t, kubeCtx)
	eval := buildSafeOperationsEvaluator(t)
	payload := map[string]any{"sub": "integration-test@company.com"}

	discovered := discoverResources(t, cc)
	t.Logf("Discovered %d resource instances in cluster %s", len(discovered), kubeCtx)

	readTools := []string{"get_resource", "list_resources", "describe_resource"}
	writeTools := []string{"apply_manifest", "patch_resource"}
	deleteTools := []string{"delete_resource", "delete_resources"}
	execTool := []string{"exec_command"}
	allTools := append(append(append(readTools, writeTools...), deleteTools...), execTool...)

	type verdict struct {
		resource discoveredResource
		tool     string
		allowed  bool
	}

	var verdicts []verdict
	var allowCount, denyCount int

	for _, res := range discovered {
		for _, tool := range allTools {
			req := AuthzRequest{
				Payload:   payload,
				Tool:      tool,
				Context:   kubeCtx,
				Namespace: res.Namespace,
				Resource: ResourceInfo{
					Group:    res.Group,
					Version:  res.Version,
					Resource: res.Resource,
					Name:     res.Name,
				},
			}

			allowed, err := eval.Evaluate(req)
			if err != nil {
				t.Errorf("Evaluate(%s on %s): %v", tool, res.fqn(), err)
				continue
			}

			verdicts = append(verdicts, verdict{resource: res, tool: tool, allowed: allowed})
			if allowed {
				allowCount++
			} else {
				denyCount++
			}
		}
	}

	t.Logf("\n=== SAFE-OPS POLICY VERDICT SUMMARY ===")
	t.Logf("Total evaluations: %d (allowed: %d, denied: %d)", len(verdicts), allowCount, denyCount)

	byResourceType := map[string]struct{ allow, deny int }{}
	for _, v := range verdicts {
		key := v.resource.Resource
		entry := byResourceType[key]
		if v.allowed {
			entry.allow++
		} else {
			entry.deny++
		}
		byResourceType[key] = entry
	}

	resourceTypes := make([]string, 0, len(byResourceType))
	for k := range byResourceType {
		resourceTypes = append(resourceTypes, k)
	}
	sort.Strings(resourceTypes)

	t.Logf("\n--- By resource type ---")
	t.Logf("%-30s %8s %8s", "RESOURCE", "ALLOWED", "DENIED")
	t.Logf("%-30s %8s %8s", strings.Repeat("-", 30), strings.Repeat("-", 8), strings.Repeat("-", 8))
	for _, rt := range resourceTypes {
		entry := byResourceType[rt]
		t.Logf("%-30s %8d %8d", rt, entry.allow, entry.deny)
	}

	byTool := map[string]struct{ allow, deny int }{}
	for _, v := range verdicts {
		entry := byTool[v.tool]
		if v.allowed {
			entry.allow++
		} else {
			entry.deny++
		}
		byTool[v.tool] = entry
	}

	toolNames := make([]string, 0, len(byTool))
	for k := range byTool {
		toolNames = append(toolNames, k)
	}
	sort.Strings(toolNames)

	t.Logf("\n--- By tool ---")
	t.Logf("%-25s %8s %8s", "TOOL", "ALLOWED", "DENIED")
	t.Logf("%-25s %8s %8s", strings.Repeat("-", 25), strings.Repeat("-", 8), strings.Repeat("-", 8))
	for _, tn := range toolNames {
		entry := byTool[tn]
		t.Logf("%-25s %8d %8d", tn, entry.allow, entry.deny)
	}

	t.Logf("\n--- Sensitive resources detail ---")
	sensitiveResources := map[string]bool{
		"secrets": true, "serviceaccounts": true,
	}
	for _, v := range verdicts {
		if sensitiveResources[v.resource.Resource] {
			status := "DENIED"
			if v.allowed {
				status = "ALLOWED"
			}
			t.Logf("  [%s] %s on %s", status, v.tool, v.resource.fqn())
		}
	}

	t.Logf("\n--- Exec verdicts ---")
	for _, v := range verdicts {
		if v.tool == "exec_command" {
			status := "DENIED"
			if v.allowed {
				status = "ALLOWED"
			}
			t.Logf("  [%s] exec_command on %s", status, v.resource.fqn())
		}
	}

	t.Logf("\n--- Delete verdicts ---")
	for _, v := range verdicts {
		if strings.HasPrefix(v.tool, "delete_") {
			status := "DENIED"
			if v.allowed {
				status = "ALLOWED"
			}
			t.Logf("  [%s] %s on %s", status, v.tool, v.resource.fqn())
		}
	}

	t.Logf("\n--- By namespace ---")
	byNS := map[string]struct{ allow, deny int }{}
	for _, v := range verdicts {
		ns := v.resource.Namespace
		if ns == "" {
			ns = "(cluster-scoped)"
		}
		entry := byNS[ns]
		if v.allowed {
			entry.allow++
		} else {
			entry.deny++
		}
		byNS[ns] = entry
	}

	nsNames := make([]string, 0, len(byNS))
	for k := range byNS {
		nsNames = append(nsNames, k)
	}
	sort.Strings(nsNames)

	t.Logf("%-30s %8s %8s", "NAMESPACE", "ALLOWED", "DENIED")
	t.Logf("%-30s %8s %8s", strings.Repeat("-", 30), strings.Repeat("-", 8), strings.Repeat("-", 8))
	for _, ns := range nsNames {
		entry := byNS[ns]
		t.Logf("%-30s %8d %8d", ns, entry.allow, entry.deny)
	}

	t.Logf("\n--- Assertions ---")

	for _, v := range verdicts {
		if v.tool == "exec_command" && v.allowed {
			t.Errorf("VIOLATION: exec_command should be denied on %s", v.resource.fqn())
		}

		if sensitiveResources[v.resource.Resource] {
			for _, rt := range readTools {
				if v.tool == rt && v.allowed {
					t.Errorf("VIOLATION: reading %s should be denied on %s", v.resource.Resource, v.resource.fqn())
				}
			}
		}

		if v.tool == "delete_resources" && v.allowed {
			t.Errorf("VIOLATION: bulk delete should be denied on %s", v.resource.fqn())
		}

		if v.tool == "delete_resource" && v.allowed {
			if v.resource.Resource != "pods" {
				t.Errorf("VIOLATION: delete_resource should only be allowed for pods, got %s on %s", v.resource.Resource, v.resource.fqn())
			}
			if !isDeleteAllowedNamespace(v.resource.Namespace) {
				t.Errorf("VIOLATION: delete_resource pod allowed in wrong namespace %q on %s", v.resource.Namespace, v.resource.fqn())
			}
		}

		if (v.tool == "apply_manifest" || v.tool == "patch_resource") && v.allowed {
			t.Errorf("VIOLATION: %s should be denied on %s", v.tool, v.resource.fqn())
		}
	}

	virtualTools := map[string]ResourceInfo{
		"list_contexts":       {Group: VirtualResourceGroup, Resource: VirtualResourceContext},
		"get_current_context": {Group: VirtualResourceGroup, Resource: VirtualResourceContext},
		"get_cluster_info":    {Group: VirtualResourceGroup, Resource: VirtualResourceClusterInfo},
		"list_api_resources":  {Group: VirtualResourceGroup, Resource: VirtualResourceAPIDiscovery},
		"list_api_versions":   {Group: VirtualResourceGroup, Resource: VirtualResourceAPIDiscovery},
		"switch_context":      {Group: VirtualResourceGroup, Resource: VirtualResourceContext},
	}

	t.Logf("\n--- Virtual MCP resources ---")
	for tool, res := range virtualTools {
		req := AuthzRequest{
			Payload:  payload,
			Tool:     tool,
			Context:  kubeCtx,
			Resource: res,
		}
		allowed, err := eval.Evaluate(req)
		if err != nil {
			t.Errorf("Evaluate(%s): %v", tool, err)
			continue
		}
		status := "DENIED"
		if allowed {
			status = "ALLOWED"
		}
		t.Logf("  [%s] %s (resource: _/%s)", status, tool, res.Resource)

		if tool == "switch_context" && allowed {
			t.Errorf("VIOLATION: switch_context should be denied")
		}
		if tool != "switch_context" && !allowed {
			t.Errorf("VIOLATION: %s should be allowed", tool)
		}
	}

	anonymousReq := AuthzRequest{
		Payload:   nil,
		Tool:      "get_resource",
		Context:   kubeCtx,
		Namespace: "default",
		Resource:  ResourceInfo{Group: "", Version: "v1", Resource: "pods"},
	}
	anonAllowed, _ := eval.Evaluate(anonymousReq)
	if anonAllowed {
		t.Error("VIOLATION: anonymous access should be denied")
	}
	t.Logf("\n  [DENIED] anonymous get_resource pods (as expected)")

	noSubReq := AuthzRequest{
		Payload:   map[string]any{"email": "user@co.com"},
		Tool:      "get_resource",
		Context:   kubeCtx,
		Namespace: "default",
		Resource:  ResourceInfo{Group: "", Version: "v1", Resource: "pods"},
	}
	noSubAllowed, _ := eval.Evaluate(noSubReq)
	if noSubAllowed {
		t.Error("VIOLATION: payload without sub should be denied")
	}
	t.Logf("  [DENIED] no-sub get_resource pods (as expected)")

	listNSReq := AuthzRequest{
		Payload:   payload,
		Tool:      "list_namespaces",
		Context:   kubeCtx,
		Namespace: "",
		Resource:  ResourceInfo{Group: "", Version: "v1", Resource: "namespaces"},
	}
	listNSAllowed, err := eval.Evaluate(listNSReq)
	if err != nil {
		t.Errorf("Evaluate(list_namespaces): %v", err)
	}
	status := "DENIED"
	if listNSAllowed {
		status = "ALLOWED"
	}
	t.Logf("  [%s] list_namespaces", status)
	if !listNSAllowed {
		t.Error("VIOLATION: list_namespaces should be allowed")
	}

	t.Logf("\n=== DONE ===")
}

func TestIntegration_DiscoveryReport(t *testing.T) {
	kubeCtx := getKubeContext(t)
	cc := connectToCluster(t, kubeCtx)
	eval := buildSafeOperationsEvaluator(t)
	payload := map[string]any{"sub": "integration-test@company.com"}
	ctx := context.Background()

	_, apiResourceLists, err := cc.clientset.Discovery().ServerGroupsAndResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		t.Fatalf("discover: %v", err)
	}

	type apiType struct {
		group, version, resource string
		namespaced               bool
	}
	var allTypes []apiType
	for _, list := range apiResourceLists {
		gv := list.GroupVersion
		group := ""
		version := gv
		if idx := strings.LastIndex(gv, "/"); idx >= 0 {
			group = gv[:idx]
			version = gv[idx+1:]
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			allTypes = append(allTypes, apiType{group, version, r.Name, r.Namespaced})
		}
	}

	nsList, _ := cc.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	sampleNS := "default"
	if len(nsList.Items) > 0 {
		sampleNS = nsList.Items[0].Name
	}

	t.Logf("\n=== API TYPE AUTHORIZATION MATRIX ===")
	t.Logf("Context: %s | Sample namespace: %s", kubeCtx, sampleNS)
	t.Logf("%-40s %-12s %-10s %-10s %-10s %-10s %-10s", "API TYPE", "SCOPED", "GET", "LIST", "APPLY", "DELETE", "EXEC")
	t.Logf("%-40s %-12s %-10s %-10s %-10s %-10s %-10s",
		strings.Repeat("-", 40), strings.Repeat("-", 12),
		strings.Repeat("-", 10), strings.Repeat("-", 10), strings.Repeat("-", 10), strings.Repeat("-", 10), strings.Repeat("-", 10))

	sort.Slice(allTypes, func(i, j int) bool {
		if allTypes[i].group != allTypes[j].group {
			return allTypes[i].group < allTypes[j].group
		}
		return allTypes[i].resource < allTypes[j].resource
	})

	check := func(tool, ns string, ri ResourceInfo) string {
		allowed, _ := eval.Evaluate(AuthzRequest{
			Payload:   payload,
			Tool:      tool,
			Context:   kubeCtx,
			Namespace: ns,
			Resource:  ri,
		})
		if allowed {
			return "ALLOW"
		}
		return "DENY"
	}

	for _, at := range allTypes {
		ns := sampleNS
		scope := "namespaced"
		if !at.namespaced {
			ns = ""
			scope = "cluster"
		}

		ri := ResourceInfo{Group: at.group, Version: at.version, Resource: at.resource, Name: "sample"}

		apiTypeName := at.resource
		if at.group != "" {
			apiTypeName = at.group + "/" + at.resource
		}

		get := check("get_resource", ns, ri)
		list := check("list_resources", ns, ResourceInfo{Group: at.group, Version: at.version, Resource: at.resource})
		apply := check("apply_manifest", ns, ri)
		del := check("delete_resource", ns, ri)
		exec := check("exec_command", ns, ri)

		t.Logf("%-40s %-12s %-10s %-10s %-10s %-10s %-10s", apiTypeName, scope, get, list, apply, del, exec)
	}
}

func TestIntegration_LiveDeleteVerdicts(t *testing.T) {
	kubeCtx := getKubeContext(t)
	cc := connectToCluster(t, kubeCtx)
	eval := buildSafeOperationsEvaluator(t)
	payload := map[string]any{"sub": "integration-test@company.com"}

	discovered := discoverResources(t, cc)
	t.Logf("Discovered %d resource instances in cluster %s", len(discovered), kubeCtx)

	var deleteAllowed, deleteDenied int
	var violations []string

	t.Logf("\n=== LIVE DELETE VERDICTS ===")
	t.Logf("Policy: delete_resource allowed ONLY for pods in aplicacion-* and default")
	t.Logf("")

	for _, res := range discovered {
		for _, tool := range []string{"delete_resource", "delete_resources"} {
			req := AuthzRequest{
				Payload:   payload,
				Tool:      tool,
				Context:   kubeCtx,
				Namespace: res.Namespace,
				Resource: ResourceInfo{
					Group:    res.Group,
					Version:  res.Version,
					Resource: res.Resource,
					Name:     res.Name,
				},
			}

			allowed, err := eval.Evaluate(req)
			if err != nil {
				t.Errorf("Evaluate(%s on %s): %v", tool, res.fqn(), err)
				continue
			}

			status := "DENIED"
			if allowed {
				status = "ALLOWED"
				deleteAllowed++
			} else {
				deleteDenied++
			}

			shouldBeAllowed := tool == "delete_resource" &&
				res.Resource == "pods" &&
				isDeleteAllowedNamespace(res.Namespace)

			if allowed && !shouldBeAllowed {
				msg := fmt.Sprintf("VIOLATION: %s ALLOWED on %s (should be denied)", tool, res.fqn())
				violations = append(violations, msg)
				t.Errorf("%s", msg)
			}
			if !allowed && shouldBeAllowed {
				msg := fmt.Sprintf("VIOLATION: %s DENIED on %s (should be allowed)", tool, res.fqn())
				violations = append(violations, msg)
				t.Errorf("%s", msg)
			}

			if allowed {
				t.Logf("  [%s] %s on %s", status, tool, res.fqn())
			}
		}
	}

	t.Logf("\n--- Non-pod deletes (should all be DENIED) ---")
	nonPodCount := 0
	for _, res := range discovered {
		if res.Resource == "pods" {
			continue
		}
		req := AuthzRequest{
			Payload:   payload,
			Tool:      "delete_resource",
			Context:   kubeCtx,
			Namespace: res.Namespace,
			Resource: ResourceInfo{
				Group:    res.Group,
				Version:  res.Version,
				Resource: res.Resource,
				Name:     res.Name,
			},
		}
		allowed, _ := eval.Evaluate(req)
		if allowed {
			t.Errorf("VIOLATION: delete_resource ALLOWED on non-pod %s", res.fqn())
		}
		nonPodCount++
	}
	t.Logf("  Tested %d non-pod resources: all DENIED", nonPodCount)

	t.Logf("\n--- Pod deletes by namespace ---")
	podsByNS := map[string][]string{}
	for _, res := range discovered {
		if res.Resource != "pods" {
			continue
		}
		podsByNS[res.Namespace] = append(podsByNS[res.Namespace], res.Name)
	}

	nsNames := make([]string, 0, len(podsByNS))
	for ns := range podsByNS {
		nsNames = append(nsNames, ns)
	}
	sort.Strings(nsNames)

	for _, ns := range nsNames {
		pods := podsByNS[ns]
		expectAllowed := isDeleteAllowedNamespace(ns)
		expectStr := "DENIED"
		if expectAllowed {
			expectStr = "ALLOWED"
		}

		for _, podName := range pods {
			req := AuthzRequest{
				Payload:   payload,
				Tool:      "delete_resource",
				Context:   kubeCtx,
				Namespace: ns,
				Resource:  ResourceInfo{Group: "", Version: "v1", Resource: "pods", Name: podName},
			}
			allowed, _ := eval.Evaluate(req)
			actual := "DENIED"
			if allowed {
				actual = "ALLOWED"
			}

			marker := "OK"
			if allowed != expectAllowed {
				marker = "FAIL"
				t.Errorf("VIOLATION: delete pod %s/%s: got %s, want %s", ns, podName, actual, expectStr)
			}
			t.Logf("  [%s] %-25s %-50s expect=%s got=%s", marker, ns, podName, expectStr, actual)
		}
	}

	t.Logf("\n--- Apply/Patch on every resource (should all be DENIED) ---")
	applyDenied := 0
	for _, res := range discovered {
		for _, tool := range []string{"apply_manifest", "patch_resource"} {
			req := AuthzRequest{
				Payload:   payload,
				Tool:      tool,
				Context:   kubeCtx,
				Namespace: res.Namespace,
				Resource: ResourceInfo{
					Group:    res.Group,
					Version:  res.Version,
					Resource: res.Resource,
					Name:     res.Name,
				},
			}
			allowed, _ := eval.Evaluate(req)
			if allowed {
				t.Errorf("VIOLATION: %s ALLOWED on %s", tool, res.fqn())
			}
			applyDenied++
		}
	}
	t.Logf("  Tested %d apply/patch attempts: all DENIED", applyDenied)

	t.Logf("\n--- Summary ---")
	t.Logf("  delete_resource/delete_resources: %d allowed, %d denied", deleteAllowed, deleteDenied)
	t.Logf("  Non-pod delete attempts: %d (all denied)", nonPodCount)
	t.Logf("  Apply/patch attempts: %d (all denied)", applyDenied)
	if len(violations) > 0 {
		t.Logf("  VIOLATIONS: %d", len(violations))
	} else {
		t.Logf("  VIOLATIONS: 0 -- policy is airtight")
	}
}
