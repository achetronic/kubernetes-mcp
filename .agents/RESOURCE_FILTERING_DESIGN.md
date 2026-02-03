# Resource-Level Authorization Design

## Goal

Extend authorization policies to allow/deny access based on:
- **Resource type** (Group, Version, Kind)
- **Resource name**
- **Resource namespace**

---

## Final Decision: Option B with full GVK in lists

Structured rules with Group/Version/Kind + Namespace/Name, all in lists for consistency.

---

## Virtual MCP Resources (group `_`)

Some tools don't operate on real Kubernetes resources. To maintain consistency in policy evaluation, these tools have **virtual resources** associated under the special group `_`.

### Tool to Resource Mapping

#### Tools with real Kubernetes resources

| Tool | Group | Kind | Notes |
|------|-------|------|-------|
| `get_resource` | (per resource) | (per resource) | GVK of requested resource |
| `list_resources` | (per resource) | (per resource) | GVK of requested resource |
| `describe_resource` | (per resource) | (per resource) | GVK of requested resource |
| `delete_resource` | (per resource) | (per resource) | GVK of requested resource |
| `delete_resources` | (per resource) | (per resource) | GVK of requested resource |
| `apply_manifest` | (per resource) | (per resource) | GVK of resource in manifest |
| `patch_resource` | (per resource) | (per resource) | GVK of resource to patch |
| `diff_manifest` | (per resource) | (per resource) | GVK of resource in manifest |
| `get_logs` | `""` | `Pod` | Always operates on Pods |
| `exec_command` | `""` | `Pod` | Always operates on Pods |
| `scale_resource` | (per resource) | (per resource) | Deployment/StatefulSet/ReplicaSet |
| `restart_rollout` | (per resource) | (per resource) | Deployment/StatefulSet/DaemonSet |
| `undo_rollout` | (per resource) | (per resource) | Deployment/StatefulSet/DaemonSet |
| `get_rollout_status` | (per resource) | (per resource) | Deployment/StatefulSet/DaemonSet |
| `list_namespaces` | `""` | `Namespace` | Real K8s resource |
| `list_events` | `""` | `Event` | Real K8s resource |
| `check_permission` | `authorization.k8s.io` | `SelfSubjectAccessReview` | Real K8s resource |
| `get_pod_metrics` | `metrics.k8s.io` | `PodMetrics` | Real K8s resource |
| `get_node_metrics` | `metrics.k8s.io` | `NodeMetrics` | Real K8s resource |

#### Tools with virtual resources (group `_`)

| Tool | Group | Kind | Description |
|------|-------|------|-------------|
| `list_api_resources` | `_` | `APIDiscovery` | Discovery of available resources |
| `list_api_versions` | `_` | `APIDiscovery` | Discovery of API versions |
| `get_cluster_info` | `_` | `ClusterInfo` | General cluster information |
| `get_current_context` | `_` | `Context` | Active MCP context |
| `list_contexts` | `_` | `Context` | Available MCP contexts |
| `switch_context` | `_` | `Context` | Switch active MCP context |

### Group `_` Characteristics

1. **Reserved prefix**: No real Kubernetes API group can start with `_`
2. **Clear semantics**: Underscore indicates "internal/virtual" (common programming convention)
3. **Consistent evaluation**: All tools go through the same authorization logic
4. **Extensible**: New internal tools follow the same pattern

---

## Complete Configuration Example

```yaml
authorization:
  allow_anonymous: false
  identity_claim: "email"
  policies:
    # SRE: Full access
    - name: "sre-full-access"
      match:
        expression: '"sre" in payload.groups'
      allow:
        tools: ["*"]
        contexts: ["*"]
        resources:
          - groups: ["*"]
            kinds: ["*"]
          - groups: ["_"]      # Virtual MCP resources
            kinds: ["*"]

    # Developers: General read access, no secrets or RBAC
    - name: "devs-read-only"
      match:
        expression: '"developers" in payload.groups'
      allow:
        tools: ["get_resource", "list_resources", "describe_resource", "get_logs"]
        contexts: ["*"]
        resources:
          # Core API (without secrets)
          - groups: [""]
            kinds: ["Pod", "Service", "ConfigMap", "PersistentVolumeClaim", "Namespace", "Event"]
          # Apps API
          - groups: ["apps"]
            kinds: ["Deployment", "StatefulSet", "DaemonSet", "ReplicaSet"]
          # Networking
          - groups: ["networking.k8s.io"]
            kinds: ["Ingress", "NetworkPolicy"]
          # Batch
          - groups: ["batch"]
            kinds: ["Job", "CronJob"]
          # Metrics (real resources)
          - groups: ["metrics.k8s.io"]
            kinds: ["PodMetrics", "NodeMetrics"]
          # Virtual MCP resources (discovery, info)
          - groups: ["_"]
            kinds: ["APIDiscovery", "ClusterInfo", "Context"]
      deny:
        resources:
          # Never secrets
          - groups: [""]
            kinds: ["Secret"]
          # Never RBAC
          - groups: ["rbac.authorization.k8s.io"]
            kinds: ["*"]
          # Never certificates
          - groups: ["certificates.k8s.io"]
            kinds: ["*"]
          # Never kube-system
          - groups: ["*"]
            kinds: ["*"]
            namespaces: ["kube-system", "kube-public"]

    # Developers: Write only in their namespaces
    - name: "devs-write-own-namespace"
      match:
        expression: '"developers" in payload.groups'
      allow:
        tools: ["apply_manifest", "patch_resource", "delete_resource", "scale_resource", "restart_rollout"]
        contexts: ["development", "staging"]
        resources:
          - groups: ["", "apps", "networking.k8s.io", "batch"]
            kinds: ["*"]
            namespaces: ["team-*"]
            names: ["*"]
      deny:
        resources:
          - groups: [""]
            kinds: ["Secret"]
```

---

## Empty Fields / Wildcards Behavior

| Field | Empty/Omitted | `["*"]` | `[""]` (empty string) |
|-------|---------------|---------|----------------------|
| `groups` | Any group | Any group | Core API only |
| `versions` | Any version | Any version | N/A |
| `kinds` | Any kind | Any kind | N/A |
| `namespaces` | Any ns + cluster-scoped | Namespaced only | Cluster-scoped only |
| `names` | Any name | Any name | N/A |

---

## Go Types

```go
// ResourceRule represents a rule for filtering resources by GVK + namespace + name
type ResourceRule struct {
    // Groups filters by API group
    // - [""] = Core API only
    // - ["_"] = Virtual MCP resources only
    // - ["*"] or omit = any group
    Groups []string `yaml:"groups,omitempty"`
    
    // Versions filters by API version (["*"] = all, omit = all)
    Versions []string `yaml:"versions,omitempty"`
    
    // Kinds filters by resource kind (["*"] = all, omit = all)
    Kinds []string `yaml:"kinds,omitempty"`
    
    // Namespaces filters by namespace (supports "prefix-*" wildcards)
    // - omit = any namespace + cluster-scoped
    // - ["*"] = any namespaced resource only
    // - [""] = cluster-scoped only
    Namespaces []string `yaml:"namespaces,omitempty"`
    
    // Names filters by resource name (supports "prefix-*" wildcards)
    Names []string `yaml:"names,omitempty"`
}

// ToolContextRule represents allowed/denied tools, contexts, and resources
type ToolContextRule struct {
    Tools              []string       `yaml:"tools,omitempty"`
    Contexts           []string       `yaml:"contexts,omitempty"`
    Resources          []ResourceRule `yaml:"resources,omitempty"`
    LabelPrefixes      []string       `yaml:"label_prefixes,omitempty"`
    AnnotationPrefixes []string       `yaml:"annotation_prefixes,omitempty"`
}
```

---

## Evaluation Logic

### General Algorithm

```
1. If JWT enabled and no token → deny (unless allow_anonymous: true)
2. Find ALL policies whose match expression is true
3. For each policy: compute effective permissions = allow - deny (for THAT policy)
4. Final merge: UNION of all effective permissions
5. If result allows tool + context + resource → allow
6. Default: deny
```

### Resource Evaluation

```
1. If resources is empty in the rule → allow any resource
2. If resources has rules:
   a. For ALLOW: resource must match AT LEAST ONE rule
   b. For DENY: if resource matches ANY deny rule → denied
3. Deny always wins over allow
```

### ResourceRule Matching

```go
func matchesResourceRule(rule ResourceRule, resource ResourceInfo, namespace string) bool {
    // Check groups ([""] = core API, ["_"] = virtual MCP)
    if len(rule.Groups) > 0 && !matchesList(rule.Groups, resource.Group) {
        return false
    }
    
    // Check versions
    if len(rule.Versions) > 0 && !matchesList(rule.Versions, resource.Version) {
        return false
    }
    
    // Check kinds
    if len(rule.Kinds) > 0 && !matchesList(rule.Kinds, resource.Kind) {
        return false
    }
    
    // Check namespaces (with wildcard support)
    if len(rule.Namespaces) > 0 && !matchesWildcardList(rule.Namespaces, namespace) {
        return false
    }
    
    // Check names (with wildcard support)
    if len(rule.Names) > 0 && !matchesWildcardList(rule.Names, resource.Name) {
        return false
    }
    
    return true
}
```

### Supported Wildcards

| Pattern | Meaning | Example |
|---------|---------|---------|
| `*` | All | `kinds: ["*"]` |
| `prefix-*` | Starts with | `namespaces: ["team-*"]` |
| `*-suffix` | Ends with | `names: ["*-config"]` |
| `*-middle-*` | Contains | `names: ["*-app-*"]` |
| `exact` | Exact match | `kinds: ["Pod"]` |

---

## Use Cases

### 1. SRE: Full access including virtual resources

```yaml
- name: "sre-full-access"
  match:
    expression: '"sre" in payload.groups'
  allow:
    tools: ["*"]
    contexts: ["*"]
    resources:
      - groups: ["*"]
        kinds: ["*"]
      - groups: ["_"]
        kinds: ["*"]
```

### 2. Developers: Read-only, no secrets, with discovery

```yaml
- name: "devs-read-with-discovery"
  match:
    expression: '"developers" in payload.groups'
  allow:
    tools: ["get_resource", "list_resources", "describe_resource"]
    contexts: ["*"]
    resources:
      - groups: ["", "apps", "networking.k8s.io"]
        kinds: ["*"]
      - groups: ["_"]
        kinds: ["APIDiscovery", "ClusterInfo", "Context"]
  deny:
    resources:
      - groups: [""]
        kinds: ["Secret"]
```

### 3. CI/CD: Apply only in specific namespaces

```yaml
- name: "cicd-deploy"
  match:
    expression: 'payload.client_id == "ci-cd-service"'
  allow:
    tools: ["apply_manifest", "get_resource", "list_resources", "diff_manifest"]
    contexts: ["staging", "production"]
    resources:
      - groups: ["", "apps", "networking.k8s.io"]
        kinds: ["Deployment", "Service", "ConfigMap", "Ingress"]
        namespaces: ["app-*"]
  deny:
    resources:
      - groups: [""]
        kinds: ["Secret"]
    tools: ["delete_resource", "delete_resources", "exec_command"]
```

### 4. Only resources with specific names

```yaml
- name: "only-own-apps"
  match:
    expression: '"app-team" in payload.groups'
  allow:
    tools: ["*"]
    contexts: ["*"]
    resources:
      - groups: ["apps"]
        kinds: ["Deployment", "StatefulSet"]
        names: ["myapp-*", "frontend-*"]
      - groups: [""]
        kinds: ["Service", "ConfigMap"]
        names: ["myapp-*", "frontend-*"]
```

---

## Code Impact

### Files to Modify

| File | Change |
|------|--------|
| `api/config_types.go` | Add `ResourceRule`, update `ToolContextRule` |
| `internal/authorization/evaluator.go` | Implement resource evaluation + virtual tools mapping |
| `internal/k8stools/*.go` | Pass full `ResourceInfo` to AuthzRequest |
| `docs/config-*.yaml` | Update examples |
| `README.md` | Document virtual resources and configuration |

### Constants for Virtual Resources

```go
package authorization

const (
    // VirtualResourceGroup is the API group for MCP virtual resources
    VirtualResourceGroup = "_"
    
    // Virtual resource kinds
    VirtualKindAPIDiscovery = "APIDiscovery"
    VirtualKindClusterInfo  = "ClusterInfo"
    VirtualKindContext      = "Context"
)

// ToolVirtualResources maps tools to their virtual resources
var ToolVirtualResources = map[string]ResourceInfo{
    "list_api_resources": {Group: VirtualResourceGroup, Kind: VirtualKindAPIDiscovery},
    "list_api_versions":  {Group: VirtualResourceGroup, Kind: VirtualKindAPIDiscovery},
    "get_cluster_info":   {Group: VirtualResourceGroup, Kind: VirtualKindClusterInfo},
    "get_current_context": {Group: VirtualResourceGroup, Kind: VirtualKindContext},
    "list_contexts":      {Group: VirtualResourceGroup, Kind: VirtualKindContext},
    "switch_context":     {Group: VirtualResourceGroup, Kind: VirtualKindContext},
}
```

---

## Next Steps

1. ✅ Design confirmed (Option B with GVK + virtual resources)
2. ✅ Virtual resources with group `_` documented
3. ✅ Cluster-scoped: `namespaces: [""]`
4. ✅ Deny always wins over allow
5. ✅ Implement types in `api/config_types.go`
6. ✅ Implement evaluation in `authorization/evaluator.go`
7. ✅ Update tools to pass `ResourceInfo`
8. ✅ Update documentation and examples
