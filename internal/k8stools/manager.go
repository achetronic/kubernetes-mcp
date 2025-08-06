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
	"log/slog"

	"kubernetes-mcp/api"
	"kubernetes-mcp/internal/authorization"
	"kubernetes-mcp/internal/kubernetes"
	"kubernetes-mcp/internal/yqutil"

	"github.com/mark3labs/mcp-go/server"
)

// Manager manages all Kubernetes MCP tools
type Manager struct {
	logger        *slog.Logger
	config        *api.Configuration
	clientManager *kubernetes.ClientManager
	authz         *authorization.Evaluator
	yq            *yqutil.Evaluator
	mcpServer     *server.MCPServer
}

// ManagerDependencies holds dependencies for the Manager
type ManagerDependencies struct {
	Logger        *slog.Logger
	Config        *api.Configuration
	ClientManager *kubernetes.ClientManager
	Authz         *authorization.Evaluator
	McpServer     *server.MCPServer
}

// NewManager creates a new k8s tools manager
func NewManager(deps ManagerDependencies) *Manager {
	return &Manager{
		logger:        deps.Logger,
		config:        deps.Config,
		clientManager: deps.ClientManager,
		authz:         deps.Authz,
		yq:            yqutil.NewEvaluator(),
		mcpServer:     deps.McpServer,
	}
}

// RegisterAll registers all Kubernetes tools with the MCP server
func (m *Manager) RegisterAll() {
	// Read tools
	m.registerGetResource()
	m.registerListResources()
	m.registerDescribeResource()

	// Modification tools
	m.registerApplyManifest()
	m.registerPatchResource()
	m.registerDeleteResource()
	m.registerDeleteResources()

	// Scaling tools
	m.registerScaleResource()

	// Rollout tools
	m.registerGetRolloutStatus()
	m.registerRestartRollout()
	m.registerUndoRollout()

	// Logs and debug
	m.registerGetLogs()
	m.registerExecCommand()

	// Cluster info
	m.registerListAPIResources()
	m.registerListAPIVersions()
	m.registerGetClusterInfo()

	// Namespace
	m.registerListNamespaces()

	// Context
	m.registerGetCurrentContext()
	m.registerListContexts()
	m.registerSwitchContext()

	// Events
	m.registerListEvents()

	// RBAC
	m.registerCheckPermission()

	// Metrics
	m.registerGetPodMetrics()
	m.registerGetNodeMetrics()

	// Diff
	m.registerDiffManifest()
}
