# TODO

Audit findings on the MCP tools — bugs to fix and behaviours to harden.
Tracked here so we can knock them down in order and tick them off as we go.

Each item links to:
- the tool / file affected
- the symptom
- the proposed fix

Status legend:
- `[ ]` pending
- `[~]` in progress
- `[x]` done

---

## Critical (functionality / security)

- [x] **B1. `apply_manifest`: Update without `resourceVersion` fails on re-apply.**
  After a successful Create, when the resource already exists the code does a
  bare `Update(obj)` whose object lacks the server-managed `resourceVersion`
  (and immutable fields like `clusterIP` for Services). Result: `Conflict` or
  silent loss of cluster-assigned fields.
  *Fix:* on `IsAlreadyExists`, GET the live object, copy `resourceVersion`
  (and preserve cluster-managed immutable fields by merging the desired spec
  on top of the live object) before `Update`. Or switch to Server-Side Apply.
  File: `internal/k8stools/tools_modify.go`.

- [x] **B2. `apply_manifest`: "already exists" detected by substring.**
  Code does `strings.Contains(err.Error(), "already exists")`. Fragile across
  locales and ambiguous if any other error contains the phrase.
  *Fix:* use `apierrors.IsAlreadyExists(err)`.
  File: `internal/k8stools/tools_modify.go`.

- [x] **B3. `patch_resource`: panic on empty patch.**
  `strings.TrimSpace(patchData)[0]` crashes the MCP server when the model
  passes `patch=""`.
  *Fix:* validate `patch` non-empty before indexing.
  File: `internal/k8stools/tools_modify.go`.

- [x] **B4. Metrics tools: authz `Resource` is wrong.**
  `get_pod_metrics` registers authz with `Resource: "podmetrics"` and
  `get_node_metrics` with `"nodemetrics"`. The actual REST plural for
  `metrics.k8s.io/v1beta1` is `pods` / `nodes`. Authorization policies that
  filter by these correct names never match.
  *Fix:* change to `pods` and `nodes` respectively.
  File: `internal/k8stools/tools_rbac_metrics.go`.

- [x] **B5. `get_rollout_status` reports bogus numbers for DaemonSet / StatefulSet.**
  Code reads only the Deployment-style status fields (`readyReplicas`,
  `availableReplicas`, etc.). DaemonSets use `numberReady`,
  `desiredNumberScheduled`, `numberAvailable`, ... and StatefulSets use
  `currentReplicas` instead of `availableReplicas`. Output is misleading.
  *Fix:* branch by GVR.Resource and read the right fields per kind. For
  DaemonSets read `desiredNumberScheduled`/`numberReady`/`updatedNumberScheduled`/
  `numberAvailable`; for StatefulSets read the SS-specific status fields.
  File: `internal/k8stools/tools_scale_rollout.go`.

- [x] **B6. `switch_context` mutates process-wide state and is dangerous in HTTP multi-client setups.**
  Client A switches to `prod`; client B (unaware) does `delete_resource` and
  hits `prod`. The handler is correct in isolation, but the design is unsafe.
  *Fix:* add a strong warning to the tool description recommending callers
  pass `context` explicitly to every tool, and document the surface caveat.
  Optional follow-up: make the switch per-MCP-session if mcp-go exposes that.
  File: `internal/k8stools/tools_context.go`.

---

## Medium (real bugs, not breakers)

- [x] **B7. `apply_manifest` accepts multi-document YAML silently and only uses the first doc.**
  *Fix:* detect multi-doc input (split by `\n---\n` or use `yaml.NewDecoder`)
  and reject with an explicit error pointing the model to one-call-per-doc.
  File: `internal/k8stools/tools_modify.go`.

- [x] **B8. `apply_manifest` success message does not distinguish create vs update.**
  Always says "Successfully applied". Misleading for the model.
  *Fix:* track which branch executed and report "created" or "updated".
  File: `internal/k8stools/tools_modify.go`.

- [x] **B9. `scale_resource` / `restart_rollout` call `Namespace("")` with namespaced kinds.**
  When the model omits `namespace`, the dynamic client invokes the
  cluster-scoped variant on a namespaced resource and fails with a confusing
  error. Also `restart_rollout` does not whitelist supported resources, so a
  caller can target `replicasets` or anything else.
  *Fix:* require non-empty `namespace` for both tools, and whitelist
  `restart_rollout` to `apps/{deployments,daemonsets,statefulsets}`.
  File: `internal/k8stools/tools_scale_rollout.go`.

- [x] **B10. `scale_resource` does not validate `replicas >= 0`.**
  *Fix:* validate before the patch. Reject negative or non-integer values.
  File: `internal/k8stools/tools_scale_rollout.go`.

- [x] **B11. `delete_resources` allows cross-namespace deletion without barrier.**
  Calling with `namespace=""` and a label selector deletes across all
  namespaces. This is the most destructive op and has no confirmation flag.
  *Fix:* reject `namespace=""` by default; require an explicit
  `all_namespaces: true` to opt in.
  File: `internal/k8stools/tools_modify.go`.

- [x] **B12. `delete_resources` has no element cap.**
  Selector that matches 10000 objects nukes them all without warning.
  *Fix:* cap from `KubernetesToolsConfig.BulkOperations.MaxResourcesPerOperation`
  (already exists in config). Pre-list with the same selector, refuse if the
  count exceeds the cap, and surface the count in the error so the caller can
  raise the cap consciously if needed.
  File: `internal/k8stools/tools_modify.go`.

- [x] **B13. `describe_resource` skips events for cluster-scoped resources.**
  Code only fetches events when `namespace != ""`. Node events live in the
  `default` namespace (their `involvedObject` references the Node, which is
  cluster-scoped). `kubectl describe node` does show them. Currently we don't.
  *Fix:* when the resource is cluster-scoped, also list events with
  `involvedObject.kind=<Kind>,involvedObject.name=<Name>` across all
  namespaces (or in `default`, matching kubectl's behaviour).
  File: `internal/k8stools/tools_read.go`.

- [x] **B14. `diff_manifest` reports systematic false positives.**
  The skip list misses common server-managed fields:
  - `metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]`
  - `metadata.finalizers`
  - `metadata.ownerReferences`
  - `metadata.deletionTimestamp`
  - cluster-assigned immutable fields (`spec.clusterIP`, `spec.clusterIPs`,
    `spec.ipFamilies`, `spec.ipFamilyPolicy` for Service; `spec.volumeName`
    for PVC; `metadata.namespace` when missing in desired).
  Also the `slicesEqual` shallow comparison flags reordering as "array
  changed" even when content is identical.
  *Fix:* extend `skipFields` (and apply it on the *current* side too — those
  fields are the ones the cluster adds), normalize unordered list fields
  (e.g., container env, ports, tolerations) before comparing, and special-case
  the immutable fields above so they stop showing up as diffs.
  File: `internal/k8stools/tools_diff.go`.

- [x] **B15. `get_logs` has no size cap.**
  `io.Copy` reads the whole stream into memory and returns it to the model
  even when the model forgets `tail_lines` / `since_seconds`. A megabyte of
  logs trashes the model context.
  *Fix:* wrap the stream with a `LimitReader` (e.g. 1 MiB), and when the cap
  is reached append a clear truncation marker like
  `\n[... output truncated at 1MiB; use tail_lines or since_seconds]`.
  File: `internal/k8stools/tools_logs_exec.go`.

- [x] **B16. `exec_command` has hardcoded 30s timeout and no output cap.**
  *Fix:* expose an optional `timeout_seconds` (default 30, cap 300) and cap
  combined stdout+stderr at 1 MiB with a truncation marker.
  File: `internal/k8stools/tools_logs_exec.go`.

- [x] **B17. `exec_command` with non-zero exit returns IsError=false.**
  When the command fails, we return success with a "Command exited with
  error" prefix. The model sees `isError=false` and assumes everything's fine.
  *Fix:* return `IsError=true` while keeping the captured output in the text.
  File: `internal/k8stools/tools_logs_exec.go`.

- [x] **B18. `list_events` filters `types` client-side and has no chronological order.**
  When `types=["Warning"]`, we still download every event and filter
  in-process. Also events come in API order, not by `lastTimestamp`.
  *Fix:* translate a single-element `types` filter into a
  `field_selector` clause `type=Warning`. After fetch, sort by
  `lastTimestamp` (or `eventTime` as fallback) descending so the most recent
  appear first. Optionally cap to a sane maximum (e.g. 200) with a notice.
  File: `internal/k8stools/tools_logs_exec.go`.

- [x] **B19. `check_permission` puts subresource into Resource field.**
  Description tells the model to use `pods/exec`, but the API expects
  `Resource: "pods", Subresource: "exec"`. With `pods/exec` the SAR returns
  unhelpful results.
  *Fix:* split on `/` inside the handler and populate `Subresource`
  separately. Document the splitting in the description.
  File: `internal/k8stools/tools_rbac_metrics.go`.

---

## Minor / cosmetic

- [ ] **B20. `get_resource` / `list_resources` accept `namespace` for cluster-scoped resources.**
  Currently the namespaced variant is called and the API returns a confusing
  404. Not catastrophic but easy to surface a clearer error.
  *Fix:* lazily resolve the resource scope via the RESTMapper and warn / drop
  `namespace` when the resource is cluster-scoped.
  Files: `internal/k8stools/tools_read.go`.
  *Status:* deferred. Costs a discovery hit on every read; revisit if it
  bites in practice.

- [x] **B21. `list_resources` has no `limit` / pagination.**
  Listing pods on a big cluster blows past the model context.
  *Fix:* add an optional `limit` int (passed through to `ListOptions.Limit`)
  and surface the `continue` token in a clear way (or for now just expose
  `limit` and warn if more results are available).
  File: `internal/k8stools/tools_read.go`.

- [x] **B22. `list_api_resources` description claims `api_group=""` matches the core API.**
  The handler treats empty as "no filter" (returns everything). Inconsistent
  with the documented behaviour.
  *Fix:* either change the code so `""` filters to core (recommended, matches
  `kubectl api-resources --api-group=""`) or fix the description. I'll go
  with code-matches-description: empty string filters to core.
  File: `internal/k8stools/tools_cluster.go`.

- [x] **B23. `get_cluster_info` reports `node_count: 0` / `namespace_count: 0` on RBAC denial.**
  Hides the difference between "no nodes" and "not allowed to see nodes".
  *Fix:* on error, either omit the field or set a sentinel like `-1` and add
  an `errors:` section explaining what was denied.
  File: `internal/k8stools/tools_cluster.go`.

- [x] **B24. `diff_manifest` `skipFields["status"]` only matches at top level.**
  A CRD with a nested `status` could in theory be filtered by accident, but
  the bigger issue is that nested `metadata.managedFields`, etc. inside
  `metadata` are correctly filtered by the dotted-key skip... but the lookup
  is exact. Consider switching to a prefix match for known root-level
  managed sections.
  *Fix:* tightened skip rules together with B14.
  File: `internal/k8stools/tools_diff.go`.

- [x] **B25. `getDeleteOptions` does not validate `propagation_policy`.**
  Any string is forwarded to the API. The API rejects invalid values but the
  message is unfriendly.
  *Fix:* validate against `Orphan` / `Background` / `Foreground` and reject
  early with a clear error.
  File: `internal/k8stools/helpers.go`.

- [x] **B26. `checkAuthorization` is a no-op when `m.authz == nil`.**
  Confirm intent: if the config has no authorization block, everything is
  permitted. This is the documented behaviour for local/dev usage but it's
  silent. We should at least log a warning at startup if HTTP transport is
  enabled without authorization.
  *Fix:* startup-time warning in `cmd/main.go` (or the Manager constructor)
  when HTTP + no authz.
  File: `cmd/main.go` (or wherever the manager is wired).

---

## Tests

For every fix above, add or update e2e coverage in `internal/k8stools/e2e_*_test.go`
to lock in the new behaviour.

Particular attention to:
- `apply_manifest` re-apply of an existing Service (B1) — was silently broken.
- `delete_resources` cross-namespace barrier (B11).
- `get_rollout_status` on real DaemonSet and StatefulSet (B5).
- `diff_manifest` of a Service with `clusterIP` (B14).
- `check_permission` with subresource `pods/exec` (B19).

---

## Out of scope (for now)

- Server-Side Apply support across the board (much bigger refactor).
- Multi-cluster / per-session context state for `switch_context` (would need
  mcp-go session API).
- Argo Rollouts undo (already deferred earlier).
- `get_pod_metrics` / `get_node_metrics` against `metrics.k8s.io` v1 vs v1beta1
  (we currently target v1beta1 hardcoded).
- **bjw-s/app-template chart 4.x → 5.x migration.** Pinned to 4.2.0 in the
  README and `chart/values.yaml`. The 5.0.0 release introduces breaking
  changes to several blocks we use:
  - `rawResources.<name>`: manifest body moves under a `manifest:` key,
    `labels`/`annotations` move under `metadata:`.
  - `defaultPodOptions.automountServiceAccountToken` now defaults to
    `false`. Need to set it to `true` (or rely on the auto-created
    ServiceAccount and explicitly opt in) for the in-cluster auth path
    to keep working.
  - A default ServiceAccount is created automatically; revisit alongside
    the in-cluster RBAC docs in the README.
  Migration is straightforward but requires touching every entry under
  `rawResources` in `chart/values.yaml` and the `defaultPodOptions` block.
