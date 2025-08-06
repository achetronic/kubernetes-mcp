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

package main

import (
	"log"
	"net/http"
	"time"

	"kubernetes-mcp/internal/authorization"
	"kubernetes-mcp/internal/globals"
	"kubernetes-mcp/internal/handlers"
	"kubernetes-mcp/internal/k8stools"
	"kubernetes-mcp/internal/kubernetes"
	"kubernetes-mcp/internal/middlewares"

	"github.com/mark3labs/mcp-go/server"
)

func main() {

	// 0. Process the configuration
	appCtx, err := globals.NewApplicationContext()
	if err != nil {
		log.Fatalf("failed creating application context: %v", err.Error())
	}

	// 1. Initialize middlewares that need it
	accessLogsMw := middlewares.NewAccessLogsMiddleware(middlewares.AccessLogsMiddlewareDependencies{
		AppCtx: appCtx,
	})

	jwtValidationMw, err := middlewares.NewJWTValidationMiddleware(middlewares.JWTValidationMiddlewareDependencies{
		AppCtx: appCtx,
	})
	if err != nil {
		appCtx.Logger.Info("failed starting JWT validation middleware", "error", err.Error())
	}

	// 2. Create a new MCP server
	mcpServer := server.NewMCPServer(
		appCtx.Config.Server.Name,
		appCtx.Config.Server.Version,
		server.WithToolCapabilities(true),
	)

	// 3. Initialize handlers for later usage
	hm := handlers.NewHandlersManager(handlers.HandlersManagerDependencies{
		AppCtx: appCtx,
	})

	// 4. Initialize Kubernetes client manager
	var clientManager *kubernetes.ClientManager
	if len(appCtx.Config.Kubernetes.Contexts) > 0 {
		clientManager, err = kubernetes.NewClientManager(&appCtx.Config.Kubernetes)
		if err != nil {
			appCtx.Logger.Error("failed creating Kubernetes client manager", "error", err.Error())
			// Continue without Kubernetes - tools will fail gracefully
		}
	} else {
		appCtx.Logger.Info("no Kubernetes contexts configured, Kubernetes tools will not be available")
	}

	// 5. Initialize authorization evaluator
	var authzEvaluator *authorization.Evaluator
	if len(appCtx.Config.Authorization.Policies) > 0 {
		authzEvaluator, err = authorization.NewEvaluator(&appCtx.Config.Authorization)
		if err != nil {
			appCtx.Logger.Error("failed creating authorization evaluator", "error", err.Error())
			// Continue without authorization - all requests will be denied by default
		}
	} else {
		appCtx.Logger.Info("no authorization policies configured")
	}

	// 6. Register Kubernetes tools
	if clientManager != nil {
		k8sManager := k8stools.NewManager(k8stools.ManagerDependencies{
			Logger:        appCtx.Logger,
			Config:        appCtx.Config,
			ClientManager: clientManager,
			Authz:         authzEvaluator,
			McpServer:     mcpServer,
		})
		k8sManager.RegisterAll()
		appCtx.Logger.Info("registered Kubernetes tools", "contexts", clientManager.ListContexts())
	}

	// 7. Wrap MCP server in a transport (stdio, HTTP, SSE)
	switch appCtx.Config.Server.Transport.Type {
	case "http":
		httpServer := server.NewStreamableHTTPServer(mcpServer,
			server.WithHeartbeatInterval(30*time.Second),
			server.WithStateLess(false))

		// Register it under a path, then add custom endpoints.
		// Custom endpoints are needed as the library is not feature-complete according to MCP spec requirements (2025-06-16)
		// Ref: https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#overview
		mux := http.NewServeMux()
		mux.Handle("/mcp", accessLogsMw.Middleware(jwtValidationMw.Middleware(httpServer)))

		if appCtx.Config.OAuthAuthorizationServer.Enabled {
			mux.Handle("/.well-known/oauth-authorization-server"+appCtx.Config.OAuthAuthorizationServer.UrlSuffix,
				accessLogsMw.Middleware(http.HandlerFunc(hm.HandleOauthAuthorizationServer)))
		}

		if appCtx.Config.OAuthProtectedResource.Enabled {
			mux.Handle("/.well-known/oauth-protected-resource"+appCtx.Config.OAuthProtectedResource.UrlSuffix,
				accessLogsMw.Middleware(http.HandlerFunc(hm.HandleOauthProtectedResources)))
		}

		// Start StreamableHTTP server
		appCtx.Logger.Info("starting StreamableHTTP server", "host", appCtx.Config.Server.Transport.HTTP.Host)
		err := http.ListenAndServe(appCtx.Config.Server.Transport.HTTP.Host, mux)
		if err != nil {
			log.Fatal(err)
		}

	default:
		// Start stdio server
		appCtx.Logger.Info("starting stdio server")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatal(err)
		}
	}
}
