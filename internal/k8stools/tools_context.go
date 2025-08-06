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
)

func (m *Manager) registerGetCurrentContext() {
	tool := mcp.NewTool("get_current_context",
		mcp.WithDescription("Gets the current Kubernetes context"),
	)
	m.mcpServer.AddTool(tool, m.handleGetCurrentContext)
}

func (m *Manager) handleGetCurrentContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Check authorization
	if err := m.checkAuthorization(request, "get_current_context", "", "", authorization.ResourceInfo{}); err != nil {
		return errorResult(err), nil
	}

	currentCtx := m.clientManager.GetCurrentContext()
	config, _ := m.clientManager.GetContextConfig(currentCtx)

	info := map[string]any{
		"name":        currentCtx,
		"description": config.Description,
	}

	yamlOutput, err := objectToYAML(info)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(yamlOutput), nil
}

func (m *Manager) registerListContexts() {
	tool := mcp.NewTool("list_contexts",
		mcp.WithDescription("Lists available Kubernetes contexts"),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.[].name' (get context names), '.[] | select(.current == true)' (get current context)")),
	)
	m.mcpServer.AddTool(tool, m.handleListContexts)
}

func (m *Manager) handleListContexts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Check authorization
	if err := m.checkAuthorization(request, "list_contexts", "", "", authorization.ResourceInfo{}); err != nil {
		return errorResult(err), nil
	}

	contexts := m.clientManager.ListContexts()
	currentCtx := m.clientManager.GetCurrentContext()

	type ContextInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Current     bool   `json:"current"`
	}

	var ctxList []ContextInfo
	for _, name := range contexts {
		config, _ := m.clientManager.GetContextConfig(name)
		ctxList = append(ctxList, ContextInfo{
			Name:        name,
			Description: config.Description,
			Current:     name == currentCtx,
		})
	}

	yamlOutput, err := objectToYAML(ctxList)
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

func (m *Manager) registerSwitchContext() {
	tool := mcp.NewTool("switch_context",
		mcp.WithDescription("Switches the active Kubernetes context"),
		mcp.WithString("context_name", mcp.Required(), mcp.Description("Name of the context to switch to")),
	)
	m.mcpServer.AddTool(tool, m.handleSwitchContext)
}

func (m *Manager) handleSwitchContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	contextName, _ := args["context_name"].(string)

	// Check authorization for both the tool and the target context
	if err := m.checkAuthorization(request, "switch_context", contextName, "", authorization.ResourceInfo{}); err != nil {
		return errorResult(err), nil
	}

	oldContext := m.clientManager.GetCurrentContext()

	if err := m.clientManager.SetCurrentContext(contextName); err != nil {
		return errorResult(err), nil
	}

	config, _ := m.clientManager.GetContextConfig(contextName)

	return successResult(fmt.Sprintf("Switched context from %s to %s\nDescription: %s", oldContext, contextName, config.Description)), nil
}
