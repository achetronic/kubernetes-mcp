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

package k8stools

import (
	"context"
	"fmt"

	"kubernetes-mcp/internal/authorization"

	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) registerListAPIResources() {
	tool := mcp.NewTool("list_api_resources",
		mcp.WithDescription("Lists available API resources in the cluster"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("api_group", mcp.Description("Filter by API group")),
		mcp.WithBoolean("namespaced", mcp.Description("Filter by namespaced resources (true/false)")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.[] | select(.namespaced == true)' (filter namespaced resources), '.[].kind' (get all kinds), 'map(select(.group == \"apps\"))' (filter by group)")),
	)
	m.mcpServer.AddTool(tool, m.handleListAPIResources)
}

func (m *Manager) handleListAPIResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	apiGroup, _ := args["api_group"].(string)
	namespacedFilter, hasNamespacedFilter := args["namespaced"].(bool)

	// Check authorization (virtual resource: _/APIDiscovery)
	if err := m.checkAuthorization(request, "list_api_resources", k8sContext, "", authorization.ResourceInfo{
		Group: authorization.VirtualResourceGroup,
		Kind:  authorization.VirtualKindAPIDiscovery,
	}); err != nil {
		return errorResult(err), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	_, apiResourceLists, err := client.Clientset.Discovery().ServerGroupsAndResources()
	if err != nil {
		// Some groups may not be available, but we can still work with what we have
		if apiResourceLists == nil {
			return errorResult(err), nil
		}
	}

	type ResourceInfo struct {
		Group      string   `json:"group"`
		Version    string   `json:"version"`
		Kind       string   `json:"kind"`
		Name       string   `json:"name"`
		Namespaced bool     `json:"namespaced"`
		Verbs      []string `json:"verbs"`
	}

	var resources []ResourceInfo

	for _, list := range apiResourceLists {
		// Parse group/version
		gv := list.GroupVersion
		group := ""
		version := gv
		if idx := len(gv) - 1; idx > 0 {
			for i := idx; i >= 0; i-- {
				if gv[i] == '/' {
					group = gv[:i]
					version = gv[i+1:]
					break
				}
			}
		}

		// Filter by API group if specified
		if apiGroup != "" && group != apiGroup {
			continue
		}

		for _, r := range list.APIResources {
			// Filter by namespaced if specified
			if hasNamespacedFilter && r.Namespaced != namespacedFilter {
				continue
			}

			resources = append(resources, ResourceInfo{
				Group:      group,
				Version:    version,
				Kind:       r.Kind,
				Name:       r.Name,
				Namespaced: r.Namespaced,
				Verbs:      r.Verbs,
			})
		}
	}

	yamlOutput, err := objectToYAML(resources)
	if err != nil {
		return errorResult(err), nil
	}

	// Apply yq expressions
	finalOutput, err := m.applyYQExpressions(yamlOutput, args)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(finalOutput), nil
}

func (m *Manager) registerListAPIVersions() {
	tool := mcp.NewTool("list_api_versions",
		mcp.WithDescription("Lists available API versions"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.groups[].name' (get group names), '.groups[] | select(.name == \"apps\")' (filter specific group)")),
	)
	m.mcpServer.AddTool(tool, m.handleListAPIVersions)
}

func (m *Manager) handleListAPIVersions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)

	// Check authorization (virtual resource: _/APIDiscovery)
	if err := m.checkAuthorization(request, "list_api_versions", k8sContext, "", authorization.ResourceInfo{
		Group: authorization.VirtualResourceGroup,
		Kind:  authorization.VirtualKindAPIDiscovery,
	}); err != nil {
		return errorResult(err), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	groups, err := client.Clientset.Discovery().ServerGroups()
	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, err := objectToYAML(groups)
	if err != nil {
		return errorResult(err), nil
	}

	// Apply yq expressions
	finalOutput, err := m.applyYQExpressions(yamlOutput, args)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(finalOutput), nil
}

func (m *Manager) registerGetClusterInfo() {
	tool := mcp.NewTool("get_cluster_info",
		mcp.WithDescription("Gets basic cluster information"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
	)
	m.mcpServer.AddTool(tool, m.handleGetClusterInfo)
}

func (m *Manager) handleGetClusterInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)

	// Check authorization (virtual resource: _/ClusterInfo)
	if err := m.checkAuthorization(request, "get_cluster_info", k8sContext, "", authorization.ResourceInfo{
		Group: authorization.VirtualResourceGroup,
		Kind:  authorization.VirtualKindClusterInfo,
	}); err != nil {
		return errorResult(err), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	// Get server version
	version, err := client.Clientset.Discovery().ServerVersion()
	if err != nil {
		return errorResult(err), nil
	}

	// Get node count
	nodes, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	nodeCount := 0
	if err == nil {
		nodeCount = len(nodes.Items)
	}

	// Get namespace count
	namespaces, err := client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	nsCount := 0
	if err == nil {
		nsCount = len(namespaces.Items)
	}

	// Get context config
	ctxConfig, _ := m.clientManager.GetContextConfig(k8sContext)

	info := map[string]any{
		"context":         k8sContext,
		"description":     ctxConfig.Description,
		"server_version":  version.GitVersion,
		"platform":        version.Platform,
		"node_count":      nodeCount,
		"namespace_count": nsCount,
		"host":            client.Config.Host,
	}

	yamlOutput, err := objectToYAML(info)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(yamlOutput), nil
}

func (m *Manager) registerListNamespaces() {
	tool := mcp.NewTool("list_namespaces",
		mcp.WithDescription("Lists namespaces"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("label_selector", mcp.Description("Label selector")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.[].name' (get all namespace names), '.[] | select(.status == \"Active\")' (filter active namespaces), '.[] | select(.allowed == true)' (filter allowed namespaces)")),
	)
	m.mcpServer.AddTool(tool, m.handleListNamespaces)
}

func (m *Manager) handleListNamespaces(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	labelSelector, _ := args["label_selector"].(string)

	// Check authorization (real K8s resource: Namespace)
	if err := m.checkAuthorization(request, "list_namespaces", k8sContext, "", authorization.ResourceInfo{
		Group:   "",
		Version: "v1",
		Kind:    "Namespace",
	}); err != nil {
		return errorResult(err), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	namespaces, err := client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return errorResult(err), nil
	}

	// Create a simplified view
	type NSInfo struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Age     string `json:"age"`
		Allowed bool   `json:"allowed"`
	}

	var nsList []NSInfo
	for _, ns := range namespaces.Items {
		nsList = append(nsList, NSInfo{
			Name:    ns.Name,
			Status:  string(ns.Status.Phase),
			Age:     fmt.Sprintf("%v", metav1.Now().Sub(ns.CreationTimestamp.Time).Round(1e9)),
			Allowed: m.clientManager.IsNamespaceAllowed(k8sContext, ns.Name),
		})
	}

	yamlOutput, err := objectToYAML(nsList)
	if err != nil {
		return errorResult(err), nil
	}

	// Apply yq expressions
	finalOutput, err := m.applyYQExpressions(yamlOutput, args)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(finalOutput), nil
}
