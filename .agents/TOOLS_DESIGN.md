# Kubernetes MCP - Tools Design

Proposal for tools to interact with Kubernetes intelligently.

## Common Parameters

### Resource Identification

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `group` | string | No | API group (e.g., `apps`, `batch`, `""` for core) |
| `version` | string | Yes | API version (e.g., `v1`, `v1beta1`) |
| `kind` | string | Yes | Resource type (e.g., `Pod`, `Deployment`) |
| `name` | string | Varies | Resource name |
| `namespace` | string | No | Namespace (empty = all or cluster-scoped) |

### Filtering and Selection

| Parameter | Type | Description |
|-----------|------|-------------|
| `field_selector` | string | Field selector (e.g., `metadata.name=foo,status.phase=Running`) |
| `label_selector` | string | Label selector (e.g., `app=nginx,env!=prod`) |

### Data Extraction

| Parameter | Type | Description |
|-----------|------|-------------|
| `yq_expressions` | []string | yq expressions applied in cascade to filter/transform output |

---

## Proposed Tools

### 1. Read and List

#### `get_resource`
Gets a specific resource by name.

```yaml
params:
  - group: string (optional)
  - version: string (required)
  - kind: string (required)
  - name: string (required)
  - namespace: string (optional)
  - yq_expressions: []string (optional)
```

**Example:** Get a specific Pod and extract its IP
```
group: ""
version: v1
kind: Pod
name: nginx-abc123
namespace: default
yq_expressions: [".status.podIP"]
```

---

#### `list_resources`
Lists resources with optional filters.

```yaml
params:
  - group: string (optional)
  - version: string (required)
  - kind: string (required)
  - namespace: string (optional, empty = all namespaces)
  - field_selector: string (optional)
  - label_selector: string (optional)
  - yq_expressions: []string (optional)
```

**Example:** List Running Pods and extract names
```
version: v1
kind: Pod
namespace: production
field_selector: "status.phase=Running"
label_selector: "app=api"
yq_expressions: [".items[].metadata.name"]
```

---

#### `describe_resource`
Gets detailed information about a resource (including events).

```yaml
params:
  - group: string (optional)
  - version: string (required)
  - kind: string (required)
  - name: string (required)
  - namespace: string (optional)
  - yq_expressions: []string (optional)
```

**Note:** Combines the resource + related events in a single output.

---

### 2. Modification

#### `apply_manifest`
Applies a YAML/JSON manifest (create or update).

```yaml
params:
  - manifest: string (required, YAML or JSON)
  - namespace: string (optional, overrides namespace in manifest)
```

**Example:**
```
manifest: |
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: my-config
  data:
    key: value
namespace: default
```

---

#### `patch_resource`
Applies a patch to an existing resource.

```yaml
params:
  - group: string (optional)
  - version: string (required)
  - kind: string (required)
  - name: string (required)
  - namespace: string (optional)
  - patch_type: string (required: "strategic", "merge", "json")
  - patch: string (required, YAML or JSON)
```

**Example:** Update Deployment image
```
group: apps
version: v1
kind: Deployment
name: nginx
namespace: default
patch_type: strategic
patch: |
  spec:
    template:
      spec:
        containers:
        - name: nginx
          image: nginx:1.25
```

---

#### `delete_resource`
Deletes a resource.

```yaml
params:
  - group: string (optional)
  - version: string (required)
  - kind: string (required)
  - name: string (required)
  - namespace: string (optional)
  - grace_period_seconds: int (optional, default: per resource)
  - propagation_policy: string (optional: "Orphan", "Background", "Foreground")
```

---

#### `delete_resources`
Deletes multiple resources with selectors.

```yaml
params:
  - group: string (optional)
  - version: string (required)
  - kind: string (required)
  - namespace: string (optional)
  - field_selector: string (optional)
  - label_selector: string (required, at least one selector)
  - grace_period_seconds: int (optional)
```

**Example:** Delete all Pods with label `temp=true`
```
version: v1
kind: Pod
namespace: default
label_selector: "temp=true"
```

---

### 3. Scaling

#### `scale_resource`
Scales a resource (Deployment, ReplicaSet, StatefulSet).

```yaml
params:
  - group: string (optional, default: "apps")
  - version: string (required)
  - kind: string (required)
  - name: string (required)
  - namespace: string (optional)
  - replicas: int (required)
```

---

### 4. Rollout Management

#### `get_rollout_status`
Gets the status of a rollout.

```yaml
params:
  - group: string (optional, default: "apps")
  - version: string (required)
  - kind: string (required: Deployment, DaemonSet, StatefulSet)
  - name: string (required)
  - namespace: string (optional)
```

---

#### `restart_rollout`
Restarts a rollout (triggers redeploy).

```yaml
params:
  - group: string (optional, default: "apps")
  - version: string (required)
  - kind: string (required: Deployment, DaemonSet, StatefulSet)
  - name: string (required)
  - namespace: string (optional)
```

---

#### `undo_rollout`
Reverts to a previous revision.

```yaml
params:
  - group: string (optional, default: "apps")
  - version: string (required)
  - kind: string (required: Deployment, DaemonSet, StatefulSet)
  - name: string (required)
  - namespace: string (optional)
  - to_revision: int (optional, default: previous revision)
```

---

### 5. Logs and Debug

#### `get_logs`
Gets logs from a Pod/Container.

```yaml
params:
  - name: string (required, Pod name)
  - namespace: string (optional)
  - container: string (optional, if multiple containers)
  - previous: bool (optional, logs from previous container)
  - since_seconds: int (optional, logs since N seconds ago)
  - tail_lines: int (optional, last N lines)
  - timestamps: bool (optional, include timestamps)
```

---

#### `exec_command`
Executes a command in a container.

```yaml
params:
  - name: string (required, Pod name)
  - namespace: string (optional)
  - container: string (optional)
  - command: []string (required)
```

**Note:** Non-interactive commands only. Configured timeout.

---

### 6. Cluster Information

#### `list_api_resources`
Lists available resources in the cluster.

```yaml
params:
  - api_group: string (optional, filter by group)
  - namespaced: bool (optional, filter by namespaced/cluster-scoped)
  - yq_expressions: []string (optional)
```

---

#### `list_api_versions`
Lists available API versions.

```yaml
params:
  - yq_expressions: []string (optional)
```

---

#### `get_cluster_info`
Basic cluster information.

```yaml
params: none
```

Returns: endpoint, version, detected providers, etc.

---

### 7. Namespace Management

#### `list_namespaces`
Lists namespaces with status.

```yaml
params:
  - label_selector: string (optional)
  - yq_expressions: []string (optional)
```

---

### 8. Context and Configuration

#### `get_current_context`
Returns the current kubeconfig context.

```yaml
params: none
```

---

#### `list_contexts`
Lists available contexts.

```yaml
params:
  - yq_expressions: []string (optional)
```

---

#### `switch_context`
Switches the active context (if allowed).

```yaml
params:
  - context_name: string (required)
```

---

### 9. Events

#### `list_events`
Lists cluster or namespace events.

```yaml
params:
  - namespace: string (optional, empty = all)
  - field_selector: string (optional, e.g., "involvedObject.name=my-pod")
  - types: []string (optional: ["Normal", "Warning"])
  - yq_expressions: []string (optional)
```

---

### 10. RBAC (Optional/Advanced)

#### `check_permission`
Checks if an action is allowed.

```yaml
params:
  - verb: string (required: "get", "list", "create", "delete", etc.)
  - group: string (optional)
  - resource: string (required)
  - name: string (optional)
  - namespace: string (optional)
```

---

### 11. Metrics

#### `get_pod_metrics`
Gets CPU/memory usage for pods.

```yaml
params:
  - namespace: string (optional)
  - name: string (optional, if empty lists all)
  - label_selector: string (optional)
  - yq_expressions: []string (optional)
```

---

#### `get_node_metrics`
Gets CPU/memory usage for nodes.

```yaml
params:
  - name: string (optional, if empty lists all)
  - label_selector: string (optional)
  - yq_expressions: []string (optional)
```

---

### 12. Diff

#### `diff_manifest`
Compares a manifest with the current cluster state.

```yaml
params:
  - manifest: string (required, YAML or JSON)
  - namespace: string (optional, override)
```

Returns: readable diff showing changes that would be applied.

---

## Tools Summary

| Tool | Category | Read | Write | yq_expressions |
|------|----------|------|-------|----------------|
| `get_resource` | Read | ✅ | ❌ | ✅ |
| `list_resources` | Read | ✅ | ❌ | ✅ |
| `describe_resource` | Read | ✅ | ❌ | ✅ |
| `apply_manifest` | Write | ❌ | ✅ | ❌ |
| `patch_resource` | Write | ❌ | ✅ | ❌ |
| `delete_resource` | Write | ❌ | ✅ | ❌ |
| `delete_resources` | Write | ❌ | ✅ | ❌ |
| `scale_resource` | Write | ❌ | ✅ | ❌ |
| `get_rollout_status` | Read | ✅ | ❌ | ❌ |
| `restart_rollout` | Write | ❌ | ✅ | ❌ |
| `undo_rollout` | Write | ❌ | ✅ | ❌ |
| `get_logs` | Read | ✅ | ❌ | ❌ |
| `exec_command` | Write | ❌ | ✅ | ❌ |
| `list_api_resources` | Read | ✅ | ❌ | ✅ |
| `list_api_versions` | Read | ✅ | ❌ | ✅ |
| `get_cluster_info` | Read | ✅ | ❌ | ❌ |
| `list_namespaces` | Read | ✅ | ❌ | ✅ |
| `get_current_context` | Read | ✅ | ❌ | ❌ |
| `list_contexts` | Read | ✅ | ❌ | ✅ |
| `switch_context` | Write | ❌ | ✅ | ❌ |
| `list_events` | Read | ✅ | ❌ | ✅ |
| `check_permission` | Read | ✅ | ❌ | ❌ |
| `get_pod_metrics` | Read | ✅ | ❌ | ✅ |
| `get_node_metrics` | Read | ✅ | ❌ | ✅ |
| `diff_manifest` | Read | ✅ | ❌ | ❌ |

**Total: 25 tools**

---

## Implementation Considerations

### yq_expressions

yq expressions are applied in cascade on the YAML output:

```go
// Pseudocode
output := resource.ToYAML()
for _, expr := range yq_expressions {
    output = yq.Eval(expr, output)
}
return output
```

Useful examples:
- `.metadata.name` - Just the name
- `.spec.containers[].image` - All images
- `select(.status.phase == "Running")` - Filter by condition
- `.items[] | {name: .metadata.name, status: .status.phase}` - Projection

### Security

1. **Destructive operations**: Confirm with `--force` or equivalent
2. **exec**: Limit commands, timeout, non-interactive
3. **context switching**: Configurable per context and filterable by user

### Errors

All tools must return clear errors:
- Resource not found
- Insufficient permissions
- Invalid parameters
- Timeout

---

## Design Decisions

### Discarded

| Feature | Reason |
|---------|--------|
| `watch_resource` | No value in an MCP, streaming has no clear use case |
| `port_forward` | MCP doesn't maintain state or persistent connections |
| `copy_to_pod` / `copy_from_pod` | Requires shared volume, adds complexity without clear benefit |

### Multi-Cluster and Permissions

The MCP must support multiple clusters with granular control. See `CONFIG_DESIGN.md` for detailed authorization policies configuration.

---

## Optional Tools (Low Priority)

| Tool | Description |
|------|-------------|
| `cordon_node` | Mark node as unschedulable |
| `drain_node` | Drain node |
| `taint_node` | Manage node taints |
| `set_annotations` | Add/modify annotations on one or more resources |
| `set_labels` | Add/modify labels on one or more resources |
| `remove_annotations` | Remove annotations from one or more resources |
| `remove_labels` | Remove labels from one or more resources |
| `wait_for_condition` | Wait for condition on resource |

---

## Bulk Operations Tools Detail

### `set_annotations`
Adds or modifies annotations on one or more resources simultaneously.

```yaml
params:
  - targets: []ResourceTarget (required, list of resources)
  - annotations: map[string]string (required, annotations to apply)
  - overwrite: bool (optional, default: true, if false doesn't overwrite existing)
```

**ResourceTarget:**
```yaml
# Option 1: Specific resource
- group: apps
  version: v1
  kind: Deployment
  name: nginx
  namespace: default

# Option 2: Selector (multiple resources of same type)
- group: apps
  version: v1
  kind: Deployment
  namespace: default  # optional
  label_selector: "app=api"

# Option 3: Multiple types in a namespace
- version: v1
  kinds: [Pod, Service, ConfigMap]
  namespace: production
  label_selector: "team=backend"
```

**Example:** Annotate all Deployments and StatefulSets of the backend team
```yaml
targets:
  - group: apps
    version: v1
    kinds: [Deployment, StatefulSet]
    namespace: production
    label_selector: "team=backend"
annotations:
  cost-center: "CC-1234"
  owner: "backend-team@company.com"
  managed-by: "kubernetes-mcp"
```

**Returns:**
```yaml
applied:
  - kind: Deployment
    name: api-server
    namespace: production
  - kind: Deployment
    name: worker
    namespace: production
  - kind: StatefulSet
    name: redis
    namespace: production
failed: []
total: 3
```

---

### `set_labels`
Adds or modifies labels on one or more resources simultaneously.

```yaml
params:
  - targets: []ResourceTarget (required, list of resources)
  - labels: map[string]string (required, labels to apply)
  - overwrite: bool (optional, default: true)
```

**Same target structure as `set_annotations`.**

**Example:** Label all pods in a namespace for migration
```yaml
targets:
  - version: v1
    kind: Pod
    namespace: legacy-app
labels:
  migration-wave: "wave-2"
  migration-date: "2026-02-15"
```

---

### `remove_annotations`
Removes annotations from one or more resources.

```yaml
params:
  - targets: []ResourceTarget (required)
  - annotation_keys: []string (required, keys to remove)
```

---

### `remove_labels`
Removes labels from one or more resources.

```yaml
params:
  - targets: []ResourceTarget (required)
  - label_keys: []string (required, keys to remove)
```

**Note:** Cannot remove labels that are Service/Deployment selectors (API will reject).

---

### Bulk Operations Security Considerations

1. **Implicit dry-run**: Before applying, calculate and show which resources will be affected
2. **Configurable limit**: Maximum resources per operation (e.g., 100)
3. **Audit log**: Record who, what, and when modifications occurred
4. **Label/annotation prefix protection**: Controlled via authorization policies (see `CONFIG_DESIGN.md`)
