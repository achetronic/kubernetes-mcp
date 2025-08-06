# AGENTS.md

Guide for AI agents working in this repository.

## Project Overview

**kubernetes-mcp** is a production-ready MCP (Model Context Protocol) server template built in Go. It provides OAuth authorization support (RFC 8414 and RFC 9728 compliant) and works with major AI providers like Claude Web, OpenAI, and local clients like Claude Desktop.

- **Language**: Go 1.24+
- **Module**: `kubernetes-mcp`
- **Primary dependency**: [mcp-go](https://github.com/mark3labs/mcp-go) (v0.43.2)

## Essential Commands

```bash
# Build binary (outputs to bin/kubernetes-mcp-{os}-{arch})
make build

# Run HTTP server (uses docs/config-http.yaml)
make run

# Format code
make fmt

# Lint code (vet)
make vet

# Run golangci-lint (auto-installs if missing)
make lint
make lint-fix

# Build Docker image
make docker-build

# Package binary as tarball
make package

# Show all available make targets
make help
```

## Project Structure

```
kubernetes-mcp/
├── cmd/
│   └── main.go              # Application entrypoint
├── api/
│   └── config_types.go      # Configuration type definitions (YAML struct tags)
├── internal/
│   ├── globals/
│   │   └── globals.go       # ApplicationContext (config, logger, context)
│   ├── config/
│   │   └── config.go        # YAML config parsing with env var expansion
│   ├── tools/
│   │   ├── tools.go         # ToolsManager - register MCP tools here
│   │   ├── tool_hello.go    # Example tool: hello_world
│   │   └── tool_whoami.go   # Example tool: whoami (JWT-aware)
│   ├── handlers/
│   │   ├── handlers.go                    # HandlersManager base
│   │   ├── oauth_authorization_server.go  # /.well-known/oauth-authorization-server
│   │   └── oauth_protected_resource.go    # /.well-known/oauth-protected-resource (RFC9728)
│   └── middlewares/
│       ├── interfaces.go          # ToolMiddleware, HttpMiddleware interfaces
│       ├── logging.go             # AccessLogsMiddleware
│       ├── jwt_validation.go      # JWTValidationMiddleware (local/external strategies)
│       ├── jwt_validation_utils.go # JWKS caching, token validation, key conversion
│       ├── utils.go               # Request scheme detection
│       └── noop.go                # NoopMiddleware (template for tool middlewares)
├── docs/
│   ├── config-http.yaml     # HTTP transport config example
│   └── config-stdio.yaml    # Stdio transport config example
├── chart/                   # Helm chart for Kubernetes deployment
│   ├── Chart.yaml
│   └── values.yaml          # Uses bjw-s app-template
└── Makefile
```

## Adding MCP Tools

Tools are the main extension point. To add a new tool:

### 1. Create Tool Handler

Create `internal/tools/tool_<name>.go`:

```go
package tools

import (
    "context"
    "github.com/mark3labs/mcp-go/mcp"
)

func (tm *ToolsManager) HandleToolMyTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    // Access arguments
    arguments := request.GetArguments()
    myParam, ok := arguments["param"].(string)
    if !ok {
        return &mcp.CallToolResult{
            Content: []mcp.Content{
                mcp.TextContent{
                    Type: "text",
                    Text: "Error: param is required",
                },
            },
            IsError: true,
        }, nil
    }

    // Access validated JWT (if middleware enabled)
    jwt := request.Header.Get(tm.dependencies.AppCtx.Config.Middleware.JWT.Validation.ForwardedHeader)

    return &mcp.CallToolResult{
        Content: []mcp.Content{
            mcp.TextContent{
                Type: "text",
                Text: "Success!",
            },
        },
    }, nil
}
```

### 2. Register Tool

Add to `internal/tools/tools.go` in `AddTools()`:

```go
func (tm *ToolsManager) AddTools() {
    // ... existing tools ...

    tool = mcp.NewTool("my_tool",
        mcp.WithDescription("Description of my tool"),
        mcp.WithString("param",
            mcp.Required(),
            mcp.Description("Parameter description"),
        ),
    )
    tm.dependencies.McpServer.AddTool(tool, tm.HandleToolMyTool)
}
```

## Adding MCP Resources

Resources follow a similar pattern. The README suggests:

1. Clone `internal/tools` to `internal/resources`
2. Rename structures (ToolsManager → ResourcesManager)
3. Add logic in `cmd/main.go` to invoke the resource manager

## Configuration

Configuration is YAML-based with environment variable expansion (`$VAR` or `${VAR}`).

### Key Config Sections

| Section | Purpose |
|---------|---------|
| `server` | Name, version, transport type (http/stdio) |
| `middleware.access_logs` | Header exclusion/redaction for logging |
| `middleware.jwt` | JWT validation (local with JWKS or external via proxy) |
| `oauth_authorization_server` | RFC 8414 endpoint config |
| `oauth_protected_resource` | RFC 9728 endpoint config |

### Transport Modes

- **HTTP** (`server.transport.type: http`): Runs as HTTP server, supports OAuth endpoints
- **Stdio** (`server.transport.type: stdio`): For local clients like Claude Desktop

### JWT Validation Strategies

- **external** (default): Assumes proxy (Istio) validated JWT, reads from `forwarded_header`
- **local**: Validates JWT using JWKS URI, supports CEL expressions for claims

## Code Patterns

### Dependency Injection

All managers use a dependencies struct pattern:

```go
type ToolsManagerDependencies struct {
    AppCtx      *globals.ApplicationContext
    McpServer   *server.MCPServer
    Middlewares []middlewares.ToolMiddleware
}

func NewToolsManager(deps ToolsManagerDependencies) *ToolsManager {
    return &ToolsManager{dependencies: deps}
}
```

### Middleware Interfaces

```go
// HTTP middleware
type HttpMiddleware interface {
    Middleware(next http.Handler) http.Handler
}

// Tool middleware (for wrapping tool handlers)
type ToolMiddleware interface {
    Middleware(next server.ToolHandlerFunc) server.ToolHandlerFunc
}
```

### Error Responses

Tools return errors within the result, not as Go errors:

```go
return &mcp.CallToolResult{
    Content: []mcp.Content{
        mcp.TextContent{Type: "text", Text: "Error: message"},
    },
    IsError: true,
}, nil
```

### Logging

Use structured logging via `slog` (JSON to stderr):

```go
tm.dependencies.AppCtx.Logger.Info("message", "key", value)
tm.dependencies.AppCtx.Logger.Error("message", "error", err.Error())
```

## OAuth Endpoints

When HTTP transport and OAuth is enabled:

- `/.well-known/oauth-authorization-server{suffix}` - Proxies OIDC config from issuer
- `/.well-known/oauth-protected-resource{suffix}` - Returns RFC 9728 compliant metadata

Both endpoints support optional URL suffix via `url_suffix` config.

## Deployment

### Local Development

```bash
# HTTP mode
make run

# Stdio mode (modify Makefile to use config-stdio.yaml, or run directly)
go run ./cmd/ --config ./docs/config-stdio.yaml
```

### Kubernetes

Uses Helm chart in `chart/` directory with [bjw-s app-template](https://github.com/bjw-s/helm-charts):

```bash
helm install kubernetes-mcp ./chart
```

The values.yaml includes templates for:
- Istio sidecar injection
- External Secrets for credentials
- HTTPRoute for gateway
- RequestAuthentication for JWT validation
- AuthorizationPolicy for access control

### Docker

```bash
make docker-build IMG=your-registry/kubernetes-mcp:tag
```

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

- `release-binaries.yaml`: Builds and uploads binaries on release (linux/darwin, amd64/arm64/386)
- `release-docker-images.yaml`: Builds and pushes Docker images

Triggered by GitHub releases or manual dispatch.

## Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/mark3labs/mcp-go` | MCP server framework |
| `github.com/golang-jwt/jwt/v5` | JWT parsing and validation |
| `github.com/google/cel-go` | CEL expressions for JWT claims |
| `gopkg.in/yaml.v3` | YAML config parsing |

## Gotchas & Notes

1. **No tests in repo**: Test files don't exist - add them if making significant changes

2. **JWT header passthrough**: When using external validation strategy, the validated JWT is expected in a header (default: `X-Validated-Jwt`). Tools access it via `request.Header.Get()`.

3. **JWKS caching**: Local JWT validation runs a background goroutine that fetches JWKS periodically (configurable via `cache_interval`)

4. **CEL expressions**: JWT claims validation uses CEL. The payload is available as `payload` object:
   ```yaml
   allow_conditions:
     - expression: 'payload.groups.exists(group, group in ["admin"])'
   ```

5. **Config env expansion**: Config values like `$JWT_SECRET` are expanded at load time via `os.ExpandEnv()`

6. **Binary naming**: Build outputs `bin/kubernetes-mcp-{GOOS}-{GOARCH}` (e.g., `kubernetes-mcp-linux-amd64`)

7. **Distroless container**: Dockerfile uses `gcr.io/distroless/static:nonroot` - no shell available in container

8. **Access logs**: Headers can be excluded or redacted (first 10 chars shown with `***`) via middleware config

9. **CORS headers**: OAuth endpoints set `Access-Control-Allow-Origin: *` - consider restricting in production

10. **MCP Sessions**: For HTTP transport with sessions, production recommendations include using a consistent hashring proxy (like [Hashrouter](https://github.com/achetronic/hashrouter))
