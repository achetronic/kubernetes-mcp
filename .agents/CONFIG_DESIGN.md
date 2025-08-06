# Kubernetes MCP - Configuration Design

Extension of the current config to support multi-cluster and tool-level RBAC.

## Philosophy

- **Reuse CEL**: Already used for JWT claims validation, use it for tool authorization too
- **Contexts as first-class citizens**: Each Kubernetes context is independent with its own configuration
- **No JWT = anonymous access**: Configurable, allows local usage without authentication
- **Deny by default**: If no rule allows, deny

---

## Full Proposed Configuration

```yaml
# MCP Server Configuration
server:
  name: "Kubernetes MCP"
  version: "0.1.0"
  transport:
    type: "http"
    http:
      host: ":8080"

# Middleware Configuration (existing, no changes)
middleware:
  access_logs:
    excluded_headers:
      - X-Excluded
    redacted_headers:
      - Authorization
      - X-Validated-Jwt

  jwt:
    enabled: true
    validation:
      strategy: "external"
      forwarded_header: "X-Validated-Jwt"
      local:
        jwks_uri: "https://keycloak.example.com/realms/mcp-servers/protocol/openid-connect/certs"
        cache_interval: "10s"
        allow_conditions:
          - expression: 'has(payload.email)'

# OAuth Configuration (existing, no changes)
oauth_authorization_server:
  enabled: true
  issuer_uri: "https://keycloak.example.com/realms/mcp-servers"

oauth_protected_resource:
  enabled: true
  resource: "https://kubernetes-mcp.example.com/mcp"
  auth_servers:
    - "https://keycloak.example.com/realms/mcp-servers"
  jwks_uri: "https://keycloak.example.com/realms/mcp-servers/protocol/openid-connect/certs"
  scopes_supported:
    - openid
    - profile
    - email
    - groups

# ============================================
# NEW: Kubernetes Configuration
# ============================================

kubernetes:
  # Default context when not specified
  default_context: "production"
  
  # Available contexts (clusters)
  contexts:
    production:
      # Kubeconfig for this context
      kubeconfig: "/etc/kubernetes/production.kubeconfig"
      # Or use a context from a shared kubeconfig
      # kubeconfig_context: "prod-cluster"
      
      # Description for the agent
      description: "Production cluster - handle with care"
      
      # Allowed namespaces (empty = all)
      allowed_namespaces: []
      
      # Denied namespaces (takes priority over allowed)
      denied_namespaces:
        - kube-system
        - kube-public
        - istio-system
      
    staging:
      kubeconfig: "/etc/kubernetes/staging.kubeconfig"
      description: "Staging cluster - safe for testing"
      allowed_namespaces: []
      denied_namespaces: []
      
    development:
      kubeconfig: "/etc/kubernetes/dev.kubeconfig"
      description: "Development cluster - free to experiment"
      allowed_namespaces: []
      denied_namespaces: []

  # Global tools configuration
  tools:
    # Limits for bulk operations
    bulk_operations:
      max_resources_per_operation: 100

# ============================================
# NEW: Authorization (RBAC for tools)
# ============================================

authorization:
  # Allow anonymous access if no JWT?
  allow_anonymous: false
  
  # JWT claim containing the identity (for logs and matching)
  identity_claim: "email"  # or "sub", "preferred_username", etc.
  
  # Authorization policies
  # ALL matching policies are evaluated and permissions are MERGED (most permissive wins)
  # Deny only restricts within its own policy
  policies:
    # Admin: full access
    - name: "cluster-admins"
      description: "Full access for SRE team"
      match:
        expression: 'payload.groups.exists(g, g == "sre-team") || payload.email.endsWith("@sre.company.com")'
      allow:
        tools: ["*"]
        contexts: ["*"]
        label_prefixes: ["*"]
        annotation_prefixes: ["*"]
    
    # Developers: access to dev/staging, read-only in prod
    - name: "developers"
      description: "Developers can read prod, write dev/staging"
      match:
        expression: 'payload.groups.exists(g, g == "developers")'
      allow:
        tools: ["*"]
        contexts: ["development", "staging"]
        # Can only modify labels/annotations in their domain
        label_prefixes: ["team.company.com/", "app.company.com/"]
        annotation_prefixes: ["team.company.com/", "app.company.com/"]
      deny:
        tools: ["delete_resource", "delete_resources", "exec_command"]
        contexts: ["production"]
        # Can never touch these prefixes (within this policy)
        label_prefixes: ["app.kubernetes.io/", "helm.sh/", "kubernetes.io/"]
        annotation_prefixes: ["kubernetes.io/"]
    
    - name: "developers-prod-readonly"
      description: "Developers read-only in production"
      match:
        expression: 'payload.groups.exists(g, g == "developers")'
      allow:
        tools:
          - "get_resource"
          - "list_resources"
          - "describe_resource"
          - "get_logs"
          - "list_events"
          - "get_rollout_status"
          - "list_namespaces"
          - "list_api_resources"
          - "list_api_versions"
          - "get_cluster_info"
          - "get_pod_metrics"
          - "get_node_metrics"
          - "check_permission"
        contexts: ["production"]
    
    # On-call: can restart and scale in prod, but never delete
    - name: "oncall-prod-operations"
      description: "On-call can restart and scale in production"
      match:
        expression: 'payload.groups.exists(g, g == "oncall") && payload.oncall_active == true'
      allow:
        tools:
          - "get_resource"
          - "list_resources"
          - "describe_resource"
          - "get_logs"
          - "list_events"
          - "get_rollout_status"
          - "restart_rollout"
          - "scale_resource"
          - "get_pod_metrics"
          - "get_node_metrics"
        contexts: ["production"]
      deny:
        tools: ["delete_resource", "delete_resources"]
        contexts: ["*"]
    
    # CI/CD service account
    - name: "ci-cd-service"
      description: "CI/CD pipelines"
      match:
        expression: 'payload.azp == "ci-cd-client" || payload.client_id == "ci-cd-client"'
      allow:
        tools:
          - "apply_manifest"
          - "get_resource"
          - "list_resources"
          - "get_rollout_status"
          - "diff_manifest"
        contexts: ["staging", "production"]
      deny:
        tools: ["delete_resource", "delete_resources", "exec_command"]
        contexts: ["*"]
    
    # Anonymous access (only if allow_anonymous: true)
    - name: "anonymous-readonly"
      description: "Anonymous users can only list in dev"
      match:
        expression: '!has(payload.sub)'
      allow:
        tools:
          - "list_resources"
          - "get_cluster_info"
          - "list_namespaces"
        contexts: ["development"]
      deny:
        tools: ["*"]
        contexts: ["staging", "production"]
    
    # Global rule: no one can exec in production (unless a previous policy already allows it)
    - name: "global-no-exec-prod"
      description: "Block exec in production for everyone not explicitly allowed"
      match:
        expression: 'true'
      deny:
        tools: ["exec_command"]
        contexts: ["production"]
```

---

## Evaluation Logic

```
1. If JWT enabled and no token → deny (unless allow_anonymous: true)
2. Find ALL policies whose match expression is true
3. For each policy: calculate effective permissions = allow - deny (from THAT policy)
4. Final merge: UNION of all effective permissions
5. If result allows tool + context + prefixes → allow
6. Default: deny
```

**Most permissive wins:** If a user is in multiple groups and one policy grants full access, they have it, even if another policy is more restrictive. Deny only restricts within its own policy.

**Example:** User in `sre-team` + `developers`
- Policy SRE: `allow(tools=*, contexts=*, prefixes=*)`
- Policy Dev: `allow(tools=*, contexts=[dev,staging], prefixes=[team.*])` - `deny(prefixes=[kubernetes.io/])`
- SRE effective permissions: everything
- Dev effective permissions: everything in dev/staging except kubernetes.io/ prefixes
- Union: **everything** (SRE provides the permissions Dev lacks)

---

## Available CEL Variables

| Variable | Type | Description |
|----------|------|-------------|
| `payload` | map | JWT claims (empty if no JWT) |
| `tool` | string | Name of the tool being invoked |
| `context` | string | Selected Kubernetes context |
| `namespace` | string | Resource namespace (if applicable) |
| `resource` | map | Resource info: `{group, version, kind, name}` |

### CEL Examples

```cel
# Specific user
payload.email == "admin@company.com"

# Specific group
payload.groups.exists(g, g == "sre-team")

# Multiple groups (OR)
payload.groups.exists(g, g in ["sre-team", "platform-team"])

# Email domain
payload.email.endsWith("@company.com")

# Custom claim
has(payload.k8s_admin) && payload.k8s_admin == true

# Combination
payload.groups.exists(g, g == "developers") && payload.department == "engineering"

# No JWT (anonymous)
!has(payload.sub)

# Specific tool in specific context
tool == "delete_resource" && context == "production"

# Specific namespace
namespace.startsWith("team-")
```

---

## Minimal Configuration (Local/Dev)

For local usage without authentication:

```yaml
server:
  name: "Kubernetes MCP"
  version: "0.1.0"
  transport:
    type: "stdio"

middleware:
  jwt:
    enabled: false

kubernetes:
  default_context: "default"
  contexts:
    default:
      # Uses default kubeconfig (~/.kube/config)
      kubeconfig: ""
      description: "Local cluster"

authorization:
  allow_anonymous: true
  policies:
    - name: "allow-all"
      match:
        expression: "true"
      allow:
        tools: ["*"]
        contexts: ["*"]
```

---

## Proposed Go Types

```go
// KubernetesContextConfig represents the configuration for a k8s context
type KubernetesContextConfig struct {
    Kubeconfig        string   `yaml:"kubeconfig,omitempty"`
    KubeconfigContext string   `yaml:"kubeconfig_context,omitempty"`
    Description       string   `yaml:"description,omitempty"`
    AllowedNamespaces []string `yaml:"allowed_namespaces,omitempty"`
    DeniedNamespaces  []string `yaml:"denied_namespaces,omitempty"`
}

// BulkOperationsConfig represents limits for bulk operations
type BulkOperationsConfig struct {
    MaxResourcesPerOperation int `yaml:"max_resources_per_operation"`
}

// KubernetesToolsConfig represents the tools configuration
type KubernetesToolsConfig struct {
    BulkOperations BulkOperationsConfig `yaml:"bulk_operations,omitempty"`
}

// KubernetesConfig represents the Kubernetes configuration
type KubernetesConfig struct {
    DefaultContext string                             `yaml:"default_context"`
    Contexts       map[string]KubernetesContextConfig `yaml:"contexts"`
    Tools          KubernetesToolsConfig              `yaml:"tools,omitempty"`
}

// AuthorizationPolicy represents an authorization policy
type AuthorizationPolicy struct {
    Name        string           `yaml:"name"`
    Description string           `yaml:"description,omitempty"`
    Match       MatchConfig      `yaml:"match"`
    Allow       *ToolContextRule `yaml:"allow,omitempty"`
    Deny        *ToolContextRule `yaml:"deny,omitempty"`
}

// MatchConfig represents a match condition
type MatchConfig struct {
    Expression string `yaml:"expression"`
}

// ToolContextRule represents allowed/denied tools, contexts, and prefixes
type ToolContextRule struct {
    Tools              []string `yaml:"tools"`
    Contexts           []string `yaml:"contexts"`
    LabelPrefixes      []string `yaml:"label_prefixes,omitempty"`
    AnnotationPrefixes []string `yaml:"annotation_prefixes,omitempty"`
}

// AuthorizationConfig represents the authorization configuration
type AuthorizationConfig struct {
    AllowAnonymous bool                  `yaml:"allow_anonymous"`
    IdentityClaim  string                `yaml:"identity_claim"`
    Policies       []AuthorizationPolicy `yaml:"policies"`
}
```
