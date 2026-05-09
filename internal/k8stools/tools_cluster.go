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
	tool := mcp.NewTool(m.toolName("list_api_resources"),
		mcp.WithDescription(`List the API resources actually served by the cluster, including CRDs.

Use this to discover the exact 'group' / 'version' / 'resource' tuple to
pass to other tools (get_resource, list_resources, ...). Returns one entry
per (Kind, Version) pair with its plural name, group, version, namespaced
flag and supported verbs.

If 'list_resources' fails with "the server could not find the requested
resource", run this tool first to confirm the GVR exists.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("api_group", mcp.Description("Restrict the listing to a single API group. Examples: 'apps', 'networking.k8s.io', 'storage.k8s.io'. Pass an empty string to match the core API only ('pods', 'configmaps', ...). Omit the parameter entirely to list every group.")),
		mcp.WithBoolean("namespaced", mcp.Description("If set, return only namespaced (true) or only cluster-scoped (false) resources. Omit for no filtering.")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied to the YAML output (a top-level array, NOT a List object — use '.[]'). Examples: '.[].name' (all plural names), '.[] | select(.namespaced == true) | .name' (namespaced names), 'map(select(.group == \"apps\"))' (apps group only).")),
	)
	m.mcpServer.AddTool(tool, m.handleListAPIResources)
}

func (m *Manager) handleListAPIResources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	apiGroup, hasAPIGroup := args["api_group"].(string)
	namespacedFilter, hasNamespacedFilter := args["namespaced"].(bool)

	// Check authorization (virtual resource: _/APIDiscovery)
	if err := m.checkAuthorization(request, "list_api_resources", k8sContext, "", authorization.ResourceInfo{
		Group:    authorization.VirtualResourceGroup,
		Resource: authorization.VirtualResourceAPIDiscovery,
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
		for i := len(gv) - 1; i >= 0; i-- {
			if gv[i] == '/' {
				group = gv[:i]
				version = gv[i+1:]
				break
			}
		}

		// Filter by API group:
		//   - omit parameter         => no filter
		//   - empty string ("")      => core API only (group == "")
		//   - non-empty string       => exact match
		if hasAPIGroup && group != apiGroup {
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
	tool := mcp.NewTool(m.toolName("list_api_versions"),
		mcp.WithDescription(`List the API groups served by the cluster and the versions available
within each group, including which version is the preferred one.

Use this when you don't know whether a CRD ships 'v1', 'v1beta1', or both.
For the actual resources within a group prefer 'list_api_resources'.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied to the APIGroupList YAML. Examples: '.groups[].name' (group names), '.groups[] | select(.name == \"apps\") | .preferredVersion.version' (preferred version of apps).")),
	)
	m.mcpServer.AddTool(tool, m.handleListAPIVersions)
}

func (m *Manager) handleListAPIVersions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)

	// Check authorization (virtual resource: _/APIDiscovery)
	if err := m.checkAuthorization(request, "list_api_versions", k8sContext, "", authorization.ResourceInfo{
		Group:    authorization.VirtualResourceGroup,
		Resource: authorization.VirtualResourceAPIDiscovery,
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
	tool := mcp.NewTool(m.toolName("get_cluster_info"),
		mcp.WithDescription(`Return a small summary of the targeted cluster: server version, API host,
node count, namespace count, and the human description configured for the
MCP context.

Useful as a smoke test before running anything destructive, and to confirm
which physical cluster a context points at.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
	)
	m.mcpServer.AddTool(tool, m.handleGetClusterInfo)
}

func (m *Manager) handleGetClusterInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)

	// Check authorization (virtual resource: _/ClusterInfo)
	if err := m.checkAuthorization(request, "get_cluster_info", k8sContext, "", authorization.ResourceInfo{
		Group:    authorization.VirtualResourceGroup,
		Resource: authorization.VirtualResourceClusterInfo,
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

	// Get node count. Use a sentinel of -1 + an 'errors' map so the model can
	// tell "no nodes" from "RBAC denied".
	infoErrors := map[string]string{}
	nodes, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	nodeCount := -1
	if err == nil {
		nodeCount = len(nodes.Items)
	} else {
		infoErrors["node_count"] = err.Error()
	}

	// Get namespace count, same treatment.
	namespaces, err := client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	nsCount := -1
	if err == nil {
		nsCount = len(namespaces.Items)
	} else {
		infoErrors["namespace_count"] = err.Error()
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
	if len(infoErrors) > 0 {
		info["errors"] = infoErrors
	}

	yamlOutput, err := objectToYAML(info)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(yamlOutput), nil
}

func (m *Manager) registerListNamespaces() {
	tool := mcp.NewTool(m.toolName("list_namespaces"),
		mcp.WithDescription(`List the namespaces in the cluster, with phase, age, and whether the
MCP authorization layer allows operating on each one.

The 'allowed' field reflects this MCP server's namespace allow/deny lists
for the current context, NOT Kubernetes RBAC. A namespace can be 'allowed:
true' here and still reject your call due to RBAC.

For full Namespace objects (labels, annotations, ...) use 'list_resources'
with resource='namespaces'.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("label_selector", mcp.Description("Kubernetes label selector. Examples: 'team=backend', 'env in (dev,staging)'.")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied to the YAML array (use '.[]' to iterate). Examples: '.[].name' (just names), '.[] | select(.status == \"Active\") | .name' (only active), '.[] | select(.allowed == true) | .name' (only allowed by MCP authz).")),
	)
	m.mcpServer.AddTool(tool, m.handleListNamespaces)
}

func (m *Manager) handleListNamespaces(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	labelSelector, _ := args["label_selector"].(string)

	// Check authorization (real K8s resource: Namespace)
	if err := m.checkAuthorization(request, "list_namespaces", k8sContext, "", authorization.ResourceInfo{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
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
