# AGENTS.md

Guide for AI agents working in this repository.

## Project Overview

**kubernetes-mcp** is a production-grade Model Context Protocol (MCP) server
that exposes a Kubernetes cluster (or several) to an LLM through a stable,
authorization-aware tool surface. It speaks both **stdio** (for desktop
clients like Claude Desktop) and **HTTP Streamable** (for remote agents),
implements **OAuth 2.1** discovery (RFC 8414 / RFC 9728), and ships its own
fine-grained RBAC layer evaluated with CEL on top of the cluster's native RBAC.

- **Language**: Go 1.25+
- **Module**: `kubernetes-mcp`
- **Primary dependency**: [mcp-go](https://github.com/mark3labs/mcp-go)
- **Tools**: 25 (read / modify / scale / rollout / logs / exec / events /
  cluster info / context / RBAC / metrics / diff)

## Essential Commands

```bash
make build               # Build binary -> bin/kubernetes-mcp-{os}-{arch}
make run                 # Run HTTP server with docs/config-http.yaml
make fmt                 # go fmt
make vet                 # go vet
make lint                # golangci-lint (auto-installs)
make lint-fix            # golangci-lint --fix
make docker-build IMG=…  # Build container image
make docker-push  IMG=…  # Push container image
make package             # Tarball the binary
make help                # All targets

# Tests
make test                # Unit tests (no cluster needed)
make kind-up             # Create the local Kind cluster used by e2e (idempotent)
make kind-down           # Delete it
make test-e2e            # Run the e2e suite (build tag 'e2e') against Kind
make test-e2e-clean      # down + up + tests + down (CI-style)
```

## Project Structure

```
kubernetes-mcp/
├── cmd/main.go                       # Entrypoint: wiring of middlewares,
│                                     #   ClientManager, Manager, transport.
├── api/config_types.go               # YAML configuration schema. Edit here
│                                     #   when adding new top-level config knobs.
├── internal/
│   ├── globals/globals.go            # ApplicationContext (config + logger)
│   ├── config/config.go              # YAML parsing with $VAR expansion
│   ├── handlers/                     # OAuth well-known endpoints (HTTP)
│   ├── middlewares/                  # ToolMiddleware / HttpMiddleware
│   │   ├── auth.go                   #   shared auth payload header (X-Auth-Payload)
│   │   ├── jwt_validation.go         #   JWT validation (JWKS + CEL allow_conditions)
│   │   ├── apikey_validation.go      #   Static API keys with attached payloads
│   │   ├── logging.go                #   AccessLogsMiddleware
│   │   ├── interfaces.go             #   Interfaces both kinds implement
│   │   └── utils.go / noop.go
│   ├── kubernetes/client.go          # ClientManager: per-context Client (Clientset
│   │                                 #   + DynamicClient + DiscoveryClient + RESTMapper).
│   │                                 #   Supports explicit kubeconfig, $KUBECONFIG,
│   │                                 #   ~/.kube/config and in-cluster, with inotify
│   │                                 #   reload and periodic discovery refresh.
│   ├── authorization/                # CEL-based RBAC for the MCP itself
│   │   ├── evaluator.go              #   Evaluator + AuthzRequest + ResourceInfo
│   │   ├── evaluator_test.go         #   Unit tests
│   │   ├── policy_safeops_test.go    #   "safe-ops" policy regression tests
│   │   └── integration_test.go       #   Cluster-discovery driven RBAC sanity
│   ├── k8stools/                     # The 25 MCP tools live here
│   │   ├── manager.go                #   Manager + RegisterAll()
│   │   ├── helpers.go                #   gvrFromArgs, validateGVR, RESTMapper
│   │   │                             #   resolvers, error/result helpers
│   │   ├── tools_read.go             #   get_resource, list_resources, describe_resource
│   │   ├── tools_modify.go           #   apply_manifest, patch_resource,
│   │   │                             #     delete_resource, delete_resources
│   │   ├── tools_scale_rollout.go    #   scale_resource, get_rollout_status,
│   │   │                             #     restart_rollout, undo_rollout
│   │   ├── tools_logs_exec.go        #   get_logs, exec_command, list_events
│   │   ├── tools_cluster.go          #   list_api_resources, list_api_versions,
│   │   │                             #     get_cluster_info, list_namespaces
│   │   ├── tools_context.go          #   get_current_context, list_contexts,
│   │   │                             #     switch_context
│   │   ├── tools_rbac_metrics.go     #   check_permission, get_pod_metrics,
│   │   │                             #     get_node_metrics
│   │   ├── tools_diff.go             #   diff_manifest
│   │   └── e2e_*_test.go             #   E2E tests (build tag 'e2e')
│   └── yqutil/evaluator.go           # yq expression engine used by yq_expressions
├── docs/
│   ├── config-http.yaml              # HTTP transport example
│   └── config-stdio.yaml             # Stdio transport example
├── chart/values.yaml                 # bjw-s app-template values for Helm install
├── .agents/                          # Internal docs (this directory)
│   ├── AGENTS.md
│   ├── CONFIG_DESIGN.md
│   ├── RESOURCE_FILTERING_DESIGN.md
│   ├── TOOLS_DESIGN.md
│   └── TODO.md
└── .github/workflows/                # release-binaries, release-docker-images,
                                      # e2e-tests
```

## Tools: GVR, never Kind

All resource-addressing tools take a **GroupVersionResource** triple, not a
Kind. The model passes:

- `group`: API group (`""` for core, `apps`, `networking.k8s.io`, ...).
- `version`: `v1`, `v1beta1`, ...
- `resource`: lowercase plural (`pods`, `deployments`, `storageclasses`,
  `ingresses`, `networkpolicies`). NOT the Kind.

`validateGVR` rejects empty `version`/`resource` and any `resource` that
starts uppercase (the most common Kind-vs-Resource mistake).

The two manifest tools (`apply_manifest`, `diff_manifest`) take YAML, parse
its `apiVersion`/`kind`, and resolve the GVR through the cluster's
discovery API via the **RESTMapper** stored in the per-context Client. The
RESTMapper is a `DeferredDiscoveryRESTMapper` backed by an in-memory cache
that is `Reset()` periodically by `ClientManager.refreshDiscoveryLoop` (see
`kubernetes.discovery.refresh_interval`, default 10m) so newly installed
CRDs become visible without restarting.

## Adding a New Tool

1. Pick the right `tools_<category>.go` file (or create one).
2. Write `register<Name>()` and `handle<Name>()`:

```go
package k8stools

import (
    "context"

    "kubernetes-mcp/internal/authorization"

    "github.com/mark3labs/mcp-go/mcp"
)

func (m *Manager) registerMyTool() {
    tool := mcp.NewTool(m.toolName("my_tool"),
        mcp.WithDescription(`Short imperative summary.

Multi-line description: when to use vs sibling tools, important caveats,
output shape ('.items[]' for List flavours).`),
        mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
        mcp.WithString("version", mcp.Required(), mcp.Description("API version, e.g. 'v1'.")),
        mcp.WithString("resource", mcp.Required(), mcp.Description("Lowercase plural ('pods', 'deployments'). NOT the Kind.")),
    )
    m.mcpServer.AddTool(tool, m.handleMyTool)
}

func (m *Manager) handleMyTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    args := request.GetArguments()

    k8sContext := m.getContextParam(args)
    name, _   := args["name"].(string)
    namespace, _ := args["namespace"].(string)
    gvr := gvrFromArgs(args)
    if err := validateGVR(gvr); err != nil {
        return errorResult(err), nil
    }

    if err := m.checkAuthorization(request, "my_tool", k8sContext, namespace, authorization.ResourceInfo{
        Group:    gvr.Group,
        Version:  gvr.Version,
        Resource: gvr.Resource,
        Name:     name,
    }); err != nil {
        return errorResult(err), nil
    }

    if namespace != "" && !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
        return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
    }

    client, err := m.clientManager.GetClient(k8sContext)
    if err != nil {
        return errorResult(err), nil
    }

    // ... do work via client.DynamicClient.Resource(gvr) / client.Clientset.

    return successResult("done"), nil
}
```

3. Register it in `manager.go::RegisterAll()`.
4. Add an entry in the relevant `e2e_*_test.go` (or create `e2e_<topic>_test.go`).
   Tests are gated behind the `e2e` build tag.

### Conventions

- Always start with `validateGVR(gvr)` for tools that take a GVR.
- Always call `checkAuthorization` (it short-circuits when no authz is configured).
- Always honour the per-context `IsNamespaceAllowed` allow/deny lists.
- Read tools take `yq_expressions []string` and pipe the YAML output through
  `m.applyYQExpressions(out, args)` at the end.
- Errors are returned as `*mcp.CallToolResult` with `IsError: true`, never as Go errors.
- Cap any unbounded reader (logs, exec output) at 1 MiB with a clear truncation marker.

## Configuration

Configuration is YAML-based with environment variable expansion (`$VAR` /
`${VAR}`) at load time.

### Sections

| Section | Purpose |
|---------|---------|
| `server` | Name, version, transport (`stdio` or `http` + host) |
| `middleware.access_logs` | Header excluded/redacted lists |
| `middleware.jwt` | JWT validation: JWKS URI, cache interval, CEL `allow_conditions` |
| `middleware.api_keys` | Static Bearer tokens with attached payload (constant-time compare) |
| `oauth_authorization_server` | RFC 8414 well-known endpoint, proxies issuer config |
| `oauth_protected_resource` | RFC 9728 well-known endpoint |
| `kubernetes.contexts` | List of named MCP contexts and their kubeconfigs |
| `kubernetes.contexts_dir` | Auto-discover kubeconfigs in a directory |
| `kubernetes.discovery.refresh_interval` | RESTMapper / discovery cache refresh (default 10m) |
| `kubernetes.tools.bulk_operations.max_resources_per_operation` | Hard cap on `delete_resources` (default 100) |
| `authorization.allow_anonymous` | Allow requests with no auth payload |
| `authorization.policies[]` | Named CEL-matched policies, each with `rules: [{effect, tools, contexts, resources}]` |

### Kubeconfig resolution

Per-context resolution order in `ClientManager.createClient`:

1. `kubernetes.contexts[].kubeconfig` if non-empty (hard fail on error).
2. `$KUBECONFIG` (single path or `:`-separated list).
3. `~/.kube/config` if it exists and is readable.
4. In-cluster credentials (`/var/run/secrets/kubernetes.io/serviceaccount`).
5. Otherwise, descriptive error mentioning each path that was tried.

The inotify watcher only registers when an explicit kubeconfig path is given.

### Authorization model

Policy schema:

```yaml
authorization:
  allow_anonymous: <bool>      # if false, requests with no payload are denied
  policies:
    - name: <string>
      description: <string>
      match:
        expression: <CEL>      # uses 'payload' (auth claims), 'tool', 'context',
                               # 'namespace', 'resource' (group/version/resource/name)
      rules:
        - effect: allow|deny
          tools: [<glob>...]
          contexts: [<glob>...]
          resources:
            - groups: [<glob>...]   # "" = core, "_" = virtual MCP
              versions: [<glob>...]
              resources: [<glob>...]
              namespaces: [<glob>...] # "" = cluster-scoped, omit = any
              names: [<glob>...]
```

Evaluation (in order):

1. No payload + `allow_anonymous=false` → deny.
2. Collect rules from every policy whose `match.expression` is true.
3. If any deny rule matches → deny (deny wins).
4. If any allow rule matches → allow.
5. Default: deny.

Glob support: `*`, `prefix-*`, `*-suffix`, `*mid*`, exact match.

Virtual resources (group `_`) cover tools that don't act on real K8s objects:
`apidiscovery` (list_api_*), `clusterinfo` (get_cluster_info), `contexts`
(get_current_context / list_contexts / switch_context).

## OAuth & HTTP transport

When `server.transport.type=http`:

- The MCP server is mounted at `/mcp` and wrapped in (in this order)
  AccessLogs → JWTValidation → APIKeyValidation.
- `/.well-known/oauth-authorization-server{suffix}` proxies the issuer's
  OIDC config when `oauth_authorization_server.enabled=true`.
- `/.well-known/oauth-protected-resource{suffix}` returns RFC 9728 metadata.
- A loud warning is logged if HTTP is enabled with no `authorization.policies`.
- The server runs **stateful** (`server.WithStateLess(false)`), so clients
  must propagate `Mcp-Session-Id` between requests.

## Testing

- **Unit tests**: `go test ./...`. Most coverage lives in `internal/authorization/`.
- **E2E tests**: `go test -tags=e2e ./internal/k8stools/...`. The build tag
  ensures `go test ./...` does not pull them in by accident. Each test
  creates a unique `kmcp-e2e-<rand>` namespace and cleans it up. Set
  `KMCP_E2E_CONTEXT` to choose the context (defaults to current-context).
- **CI**: `.github/workflows/e2e-tests.yaml` runs the suite on PRs and
  pushes to `master` against a fresh Kind cluster via `helm/kind-action`.

## Code Patterns

### Dependency Injection

Managers take a `<Name>Dependencies` struct.

```go
type ManagerDependencies struct {
    Logger        *slog.Logger
    Config        *api.Configuration
    ClientManager *kubernetes.ClientManager
    Authz         *authorization.Evaluator
    McpServer     *server.MCPServer
    ToolPrefix    string
}
```

### Middleware interfaces

```go
type HttpMiddleware interface { Middleware(next http.Handler) http.Handler }
type ToolMiddleware interface { Middleware(next server.ToolHandlerFunc) server.ToolHandlerFunc }
```

### Auth payload header

JWT validation and API key validation both serialize the auth claims as
JSON, hex-encode them, and set `X-Auth-Payload` on the inbound request.
Tools read it back in `extractAuthPayload` and feed the resulting map to
the CEL evaluator as the `payload` variable.

### Logging

`slog` (JSON to stderr by default).

```go
m.logger.Info("registered Kubernetes tools", "contexts", contexts)
m.logger.Error("failed to reload kubernetes client", "context", name, "error", err)
```

## Deployment

### Local

```bash
make run                                 # HTTP, uses docs/config-http.yaml
go run ./cmd/ --config docs/config-stdio.yaml
```

### Kubernetes (Helm)

```bash
helm repo add bjw-s https://bjw-s-labs.github.io/helm-charts
helm install kubernetes-mcp bjw-s/app-template -f chart/values.yaml \
  -n kubernetes-mcp --create-namespace
```

The chart values include Istio sidecar injection, an ExternalSecret
template for credentials, an HTTPRoute template for the gateway, and
RequestAuthentication/AuthorizationPolicy templates. RBAC for the in-cluster
ServiceAccount is intentionally NOT in the chart values — apply your own
ClusterRole/ClusterRoleBinding scoped to your needs.

### Docker

```bash
make docker-build IMG=your-registry/kubernetes-mcp:tag
make docker-push  IMG=your-registry/kubernetes-mcp:tag
```

## CI/CD

`.github/workflows/`:

- `release-binaries.yaml`: cross-compile and upload binaries on release.
- `release-docker-images.yaml`: build & push container images on release.
- `e2e-tests.yaml`: run the full e2e suite on every PR / push to master.

## Gotchas & Notes

1. **GVR, not Kind**: every resource-addressing tool takes `group`,
   `version`, `resource` (lowercase plural). The `apply_manifest` /
   `diff_manifest` tools accept manifests with `kind:` and resolve the GVR
   via the cluster's RESTMapper.

2. **Discovery refresh**: the per-Client `RESTMapper.Reset()` runs every
   `kubernetes.discovery.refresh_interval` (default 10m). Newly installed
   CRDs become usable after that interval (or the next inotify-driven
   client reload).

3. **`apply_manifest` semantics**: tries `Create`; on `IsAlreadyExists`,
   `GET`s the live object, copies `resourceVersion` and immutable fields
   (`Service.spec.clusterIP`, `PVC.spec.volumeName`, ...), then `Update`s.
   Reports "Successfully created" vs "Successfully updated" so the model
   knows what happened. Multi-document YAML is rejected explicitly.

4. **`delete_resources` safeties**: requires `namespace` OR
   `all_namespaces=true` (mutually exclusive); pre-lists with the
   selector and refuses to act if matches > bulk cap.

5. **`undo_rollout`** supports `apps/{deployments,statefulsets,daemonsets}`.
   Default behaviour with `to_revision=0` is N-1 (kubectl-compatible);
   passing the current revision returns a "nothing to do" error. Deployments
   walk ReplicaSet history; StatefulSets/DaemonSets walk
   ControllerRevision history and re-apply `data.raw` as a strategic
   merge patch (matches kubectl's behaviour byte-for-byte).

6. **`get_logs` / `exec_command` caps**: 1 MiB hard cap on output, with a
   visible truncation marker. `exec_command` exposes a `timeout_seconds`
   parameter (1..300, default 30).

7. **`switch_context` is process-wide** in the current implementation. In
   HTTP multi-client setups prefer passing `context` explicitly to each
   destructive tool to avoid one client's switch surprising another.

8. **Stateful HTTP**: the server runs with `WithStateLess(false)`. Clients
   that don't propagate `Mcp-Session-Id` get `400 Invalid session ID`.

9. **JWT header passthrough**: external JWT validation reads from
   `forwarded_header` (default `X-Validated-Jwt`); the auth payload
   handed to CEL is parsed once by the middleware and stored under
   `X-Auth-Payload` (hex-encoded JSON).

10. **CORS**: OAuth well-known endpoints set `Access-Control-Allow-Origin: *`.
    Restrict in production through your gateway.

11. **MCP sessions in production**: for HTTP at scale, route by session
    affinity (e.g. [Hashrouter](https://github.com/achetronic/hashrouter))
    so a session's follow-up requests land on the same replica.

12. **Authorization is checked BEFORE Kubernetes RBAC** — both layers must
    allow the call. `check_permission` only inspects K8s RBAC, not the MCP layer.
