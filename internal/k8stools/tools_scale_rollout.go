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
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"kubernetes-mcp/internal/authorization"
	"kubernetes-mcp/internal/kubernetes"

	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func (m *Manager) registerScaleResource() {
	tool := mcp.NewTool(m.toolName("scale_resource"),
		mcp.WithDescription(`Set the number of replicas of a scalable workload (Deployment,
StatefulSet, ReplicaSet).

Equivalent to 'kubectl scale --replicas=N'. Setting replicas to 0 stops
the workload without deleting it; restoring the value brings it back.

DaemonSets are NOT supported: they have no 'spec.replicas' (one Pod per
node) and a patch on the field would be silently ignored by the
controller. The tool rejects DaemonSet GVRs explicitly so the call does
not look successful while doing nothing.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Defaults to 'apps'.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, typically 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Lowercase plural: 'deployments', 'statefulsets', 'replicasets'. NOT 'daemonsets' (cannot be scaled). NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the workload to scale.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the workload lives. Required (these kinds are namespaced).")),
		mcp.WithNumber("replicas", mcp.Required(), mcp.Description("Desired replica count. Must be an integer >= 0. Use 0 to stop the workload without deleting it.")),
	)
	m.mcpServer.AddTool(tool, m.handleScaleResource)
}

func (m *Manager) handleScaleResource(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	group, _ := args["group"].(string)
	if group == "" {
		group = "apps"
	}
	version, _ := args["version"].(string)
	resource, _ := args["resource"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	replicas, _ := args["replicas"].(float64)

	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}
	if namespace == "" {
		return errorResult(fmt.Errorf("namespace is required for %s", gvr.Resource)), nil
	}
	if replicas < 0 || replicas != float64(int64(replicas)) {
		return errorResult(fmt.Errorf("replicas must be a non-negative integer, got %v", replicas)), nil
	}
	if gvr.Group != "apps" || !scaleSupportedResource(gvr.Resource) {
		// DaemonSets are intentionally rejected: they have no spec.replicas
		// and a merge patch on it is silently ignored by the controller,
		// which would make the tool look successful while doing nothing.
		return errorResult(fmt.Errorf("scale_resource is only supported for apps/{deployments,statefulsets,replicasets}; got %s/%s", gvr.Group, gvr.Resource)), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "scale_resource", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
		Name:     name,
	}); err != nil {
		return errorResult(err), nil
	}

	if !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	// Use patch to scale
	patch := map[string]any{
		"spec": map[string]any{
			"replicas": int32(replicas),
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return errorResult(err), nil
	}

	result, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, err := objectToYAML(result)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully scaled %s/%s to %d replicas\n\n%s", gvr.Resource, name, int(replicas), yamlOutput)), nil
}

func (m *Manager) registerGetRolloutStatus() {
	tool := mcp.NewTool(m.toolName("get_rollout_status"),
		mcp.WithDescription(`Inspect the progress of a rollout for a Deployment, DaemonSet or
StatefulSet.

Reports desired vs ready / updated / available replicas, the observed
generation (so you can see if the controller has picked up your latest
change), and the resource's status conditions. Use this after applying
a change to confirm the rollout has converged.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Defaults to 'apps'.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, typically 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Lowercase plural: 'deployments', 'daemonsets', 'statefulsets'. NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the workload.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the workload lives.")),
	)
	m.mcpServer.AddTool(tool, m.handleGetRolloutStatus)
}

func (m *Manager) handleGetRolloutStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	group, _ := args["group"].(string)
	if group == "" {
		group = "apps"
	}
	version, _ := args["version"].(string)
	resource, _ := args["resource"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}
	if namespace == "" {
		return errorResult(fmt.Errorf("namespace is required for %s", gvr.Resource)), nil
	}
	if gvr.Group != "apps" || !rolloutSupportedResource(gvr.Resource) {
		return errorResult(fmt.Errorf("get_rollout_status is only supported for apps/{deployments,statefulsets,daemonsets}; got %s/%s", gvr.Group, gvr.Resource)), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "get_rollout_status", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
		Name:     name,
	}); err != nil {
		return errorResult(err), nil
	}

	if !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	obj, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	statusText := formatRolloutStatus(obj, gvr, name)
	return successResult(statusText), nil
}

// formatRolloutStatus renders a kind-aware rollout summary. Deployments,
// StatefulSets and DaemonSets each expose a different set of status fields;
// a one-size-fits-all reader (the previous implementation) returned 0s for
// the kinds it did not match.
func formatRolloutStatus(obj *unstructured.Unstructured, gvr schema.GroupVersionResource, name string) string {
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	generation := obj.GetGeneration()
	observedGeneration, _, _ := unstructured.NestedInt64(status, "observedGeneration")

	var (
		desired, ready, updated, available int64
	)

	switch gvr.Resource {
	case "daemonsets":
		desired, _, _ = unstructured.NestedInt64(status, "desiredNumberScheduled")
		ready, _, _ = unstructured.NestedInt64(status, "numberReady")
		updated, _, _ = unstructured.NestedInt64(status, "updatedNumberScheduled")
		available, _, _ = unstructured.NestedInt64(status, "numberAvailable")
	case "statefulsets":
		desired, _, _ = unstructured.NestedInt64(spec, "replicas")
		ready, _, _ = unstructured.NestedInt64(status, "readyReplicas")
		updated, _, _ = unstructured.NestedInt64(status, "updatedReplicas")
		// StatefulSets do not report 'availableReplicas' in older versions; in
		// recent versions they do (1.22+). Try and fall back to readyReplicas.
		availableField, found, _ := unstructured.NestedInt64(status, "availableReplicas")
		if found {
			available = availableField
		} else {
			available = ready
		}
	default: // deployments (the public handler restricts to the three apps/v1
		// rollout kinds; this branch covers Deployments and acts as a safe
		// fallback for any future addition that uses replicas-style status).
		desired, _, _ = unstructured.NestedInt64(spec, "replicas")
		ready, _, _ = unstructured.NestedInt64(status, "readyReplicas")
		updated, _, _ = unstructured.NestedInt64(status, "updatedReplicas")
		available, _, _ = unstructured.NestedInt64(status, "availableReplicas")
	}

	statusText := fmt.Sprintf(`Rollout Status for %s/%s:
  Desired:    %d
  Ready:      %d
  Updated:    %d
  Available:  %d
  Generation: %d (observed: %d)
  Synced:     %v`,
		gvr.Resource, name,
		desired, ready, updated, available,
		generation, observedGeneration,
		generation == observedGeneration,
	)

	conditions, found, _ := unstructured.NestedSlice(status, "conditions")
	if found && len(conditions) > 0 {
		statusText += "\n\nConditions:"
		for _, c := range conditions {
			if cond, ok := c.(map[string]any); ok {
				condType, _ := cond["type"].(string)
				condStatus, _ := cond["status"].(string)
				message, _ := cond["message"].(string)
				statusText += fmt.Sprintf("\n  - %s: %s (%s)", condType, condStatus, message)
			}
		}
	}

	return statusText
}

func (m *Manager) registerRestartRollout() {
	tool := mcp.NewTool(m.toolName("restart_rollout"),
		mcp.WithDescription(`Trigger a rolling restart of a Deployment, DaemonSet or StatefulSet.

Equivalent to 'kubectl rollout restart'. Implementation: writes a
'kubectl.kubernetes.io/restartedAt' annotation on the pod template, which
forces the controller to recreate the Pods using the controlled rollout
strategy (no downtime if maxSurge / maxUnavailable are sane).

Useful to pick up new images with the same tag, refresh secrets mounted
as files, or clear a transient bad state without changing the spec.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Defaults to 'apps'.")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, typically 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("Lowercase plural: 'deployments', 'daemonsets', 'statefulsets'. NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the workload to restart.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the workload lives.")),
	)
	m.mcpServer.AddTool(tool, m.handleRestartRollout)
}

func (m *Manager) handleRestartRollout(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	group, _ := args["group"].(string)
	if group == "" {
		group = "apps"
	}
	version, _ := args["version"].(string)
	resource, _ := args["resource"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}
	if namespace == "" {
		return errorResult(fmt.Errorf("namespace is required for %s", gvr.Resource)), nil
	}
	if gvr.Group != "apps" || !rolloutSupportedResource(gvr.Resource) {
		return errorResult(fmt.Errorf("restart_rollout is only supported for apps/{deployments,statefulsets,daemonsets}; got %s/%s", gvr.Group, gvr.Resource)), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "restart_rollout", k8sContext, namespace, authorization.ResourceInfo{
		Group:    gvr.Group,
		Version:  gvr.Version,
		Resource: gvr.Resource,
		Name:     name,
	}); err != nil {
		return errorResult(err), nil
	}

	if !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	// Patch with restart annotation
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339),
					},
				},
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return errorResult(err), nil
	}

	_, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully triggered restart for %s/%s", gvr.Resource, name)), nil
}

// rolloutSupportedResource reports whether a resource has a meaningful rollout
// (deployments, statefulsets, daemonsets). Used to whitelist restart_rollout
// and get_rollout_status.
func rolloutSupportedResource(resource string) bool {
	switch resource {
	case "deployments", "statefulsets", "daemonsets":
		return true
	}
	return false
}

// scaleSupportedResource reports whether a resource has a 'spec.replicas'
// field that the apps/v1 controllers honour. DaemonSets are deliberately
// excluded: their cardinality is one-per-node and a 'spec.replicas' patch on
// them is silently ignored.
func scaleSupportedResource(resource string) bool {
	switch resource {
	case "deployments", "statefulsets", "replicasets":
		return true
	}
	return false
}

func (m *Manager) registerUndoRollout() {
	tool := mcp.NewTool(m.toolName("undo_rollout"),
		mcp.WithDescription(`Roll a workload back to a previous revision.

Supported resources (group 'apps'):
  - 'deployments'  — uses ReplicaSet history
  - 'statefulsets' — uses ControllerRevision history
  - 'daemonsets'   — uses ControllerRevision history

Behaviour:
  - 'to_revision' omitted or 0: rolls back to the revision immediately
    BEFORE the current one (revision N-1), matching 'kubectl rollout undo'.
  - 'to_revision' set: rolls back to that exact revision number. The call
    fails if the requested revision is the current one (no-op) or unknown.

The pod template of the target revision is applied via a strategic-merge
patch on 'spec.template'. Labels and selectors are not touched.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("group", mcp.Description("API group. Must be 'apps' (default).")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version, typically 'v1'.")),
		mcp.WithString("resource", mcp.Required(), mcp.Description("One of 'deployments', 'statefulsets', 'daemonsets'. Lowercase plural, NOT the Kind.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the workload to roll back.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the workload lives.")),
		mcp.WithNumber("to_revision", mcp.Description("Specific revision number to roll back to. Omit or 0 to roll back to the revision immediately before the current one (kubectl-compatible default).")),
	)
	m.mcpServer.AddTool(tool, m.handleUndoRollout)
}

func (m *Manager) handleUndoRollout(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	group, _ := args["group"].(string)
	if group == "" {
		group = "apps"
	}
	version, _ := args["version"].(string)
	resource, _ := args["resource"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	toRevision, _ := args["to_revision"].(float64)

	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	if err := validateGVR(gvr); err != nil {
		return errorResult(err), nil
	}

	// Check authorization
	if err := m.checkAuthorization(request, "undo_rollout", k8sContext, namespace, authorization.ResourceInfo{
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

	if gvr.Group != "apps" {
		return errorResult(fmt.Errorf("undo rollout is only supported for apps/{deployments,statefulsets,daemonsets}; got %s/%s", gvr.Group, gvr.Resource)), nil
	}

	switch gvr.Resource {
	case "deployments":
		return m.undoDeploymentRollout(ctx, client, gvr, namespace, name, int64(toRevision))
	case "statefulsets", "daemonsets":
		return m.undoControllerRevisionRollout(ctx, client, gvr, namespace, name, int64(toRevision))
	default:
		return errorResult(fmt.Errorf("undo rollout is only supported for apps/{deployments,statefulsets,daemonsets}; got %s/%s", gvr.Group, gvr.Resource)), nil
	}
}

// Annotations preserved on the Deployment when rolling back: kubectl propagates
// these from the current object instead of restoring them from the target RS.
// Mirrors kubectl/pkg/polymorphichelpers/rollback.go:annotationsToSkip.
var deploymentRollbackAnnotationsToSkip = map[string]bool{
	"kubectl.kubernetes.io/last-applied-configuration": true,
	"deployment.kubernetes.io/revision":                true,
	"deployment.kubernetes.io/revision-history":        true,
	"deployment.kubernetes.io/desired-replicas":        true,
	"deployment.kubernetes.io/max-replicas":            true,
	"deprecated.deployment.rollback.to":                true,
}

// podTemplateHashLabel is the label the Deployment controller adds to every
// ReplicaSet's pod template; it must be stripped before re-applying that
// template onto the Deployment, otherwise the Deployment ends up with a
// pinned hash and stops rolling out.
const podTemplateHashLabel = "pod-template-hash"

// undoDeploymentRollout implements rollback for apps/v1 Deployments by walking
// the ReplicaSets owned by the deployment and selecting the target revision.
//
// The patch we send mirrors `kubectl rollout undo` byte-for-byte: an RFC 6902
// JSON Patch with two replace operations on /spec/template and
// /metadata/annotations. This guarantees that containers, init containers,
// volumes etc. that exist only in the current template (and not in the target
// revision) are dropped, instead of being silently merged in by a strategic
// merge patch.
func (m *Manager) undoDeploymentRollout(
	ctx context.Context,
	client *kubernetes.Client,
	gvr schema.GroupVersionResource,
	namespace, name string,
	toRevision int64,
) (*mcp.CallToolResult, error) {

	deployment, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	// Read the deployment's current revision from its 'deployment.kubernetes.io/revision'
	// annotation. We use a pointer-vs-zero distinction so we can tell "annotation
	// missing" (currentRevision==nil → fall back to the highest history) apart
	// from "annotation present and equal to 0" (corrupt or pre-revision deployment).
	var currentRevision *int64
	if rev := nestedString(deployment.Object, "metadata", "annotations", "deployment.kubernetes.io/revision"); rev != "" {
		if parsed, err := strconv.ParseInt(rev, 10, 64); err == nil {
			currentRevision = &parsed
		}
	}

	rsGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
	rsList, err := client.DynamicClient.Resource(rsGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	type rsRevision struct {
		revision int64
		obj      *unstructured.Unstructured
	}

	deploymentUID := nestedString(deployment.Object, "metadata", "uid")

	var history []rsRevision
	for i := range rsList.Items {
		item := &rsList.Items[i]
		if !ownedBy(item.Object, deploymentUID) {
			continue
		}
		revStr := nestedString(item.Object, "metadata", "annotations", "deployment.kubernetes.io/revision")
		if revStr == "" {
			continue
		}
		rev, err := strconv.ParseInt(revStr, 10, 64)
		if err != nil {
			continue
		}
		history = append(history, rsRevision{revision: rev, obj: item})
	}

	if len(history) == 0 {
		return errorResult(fmt.Errorf("no rollout history found for deployment %s/%s", namespace, name)), nil
	}

	// Sort newest first.
	sort.Slice(history, func(i, j int) bool { return history[i].revision > history[j].revision })

	// If the deployment annotation was missing/unparseable, treat the highest
	// revision as the current one (kubectl-compatible fallback).
	effectiveCurrent := history[0].revision
	if currentRevision != nil {
		effectiveCurrent = *currentRevision
	}

	target, err := pickTargetRevision(history, effectiveCurrent, toRevision, func(r rsRevision) int64 { return r.revision })
	if err != nil {
		return errorResult(err), nil
	}

	patchBytes, err := buildDeploymentRollbackPatch(deployment, target.obj)
	if err != nil {
		return errorResult(err), nil
	}

	if _, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.JSONPatchType, patchBytes, metav1.PatchOptions{}); err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully rolled back deployment %s/%s to revision %d", namespace, name, target.revision)), nil
}

// buildDeploymentRollbackPatch constructs the kubectl-compatible JSON Patch.
// It mirrors getDeploymentPatch from
// k8s.io/kubectl/pkg/polymorphichelpers/rollback.go:
//
//   - /spec/template ← target ReplicaSet's spec.template, with the
//     'pod-template-hash' label stripped from template.metadata.labels.
//   - /metadata/annotations ← rebuilt from scratch: keys in
//     deploymentRollbackAnnotationsToSkip carried over from the current
//     Deployment, all other annotations taken from the target RS.
func buildDeploymentRollbackPatch(deployment, rs *unstructured.Unstructured) ([]byte, error) {
	template, found, err := unstructured.NestedMap(rs.Object, "spec", "template")
	if err != nil || !found {
		return nil, fmt.Errorf("target replicaset %s has no spec.template", rs.GetName())
	}
	// Deep-copy is implicit: NestedMap returns a freshly built map. Drop the
	// pod-template-hash label so the deployment isn't pinned to a stale hash.
	if templateMeta, ok := template["metadata"].(map[string]any); ok {
		if labels, ok := templateMeta["labels"].(map[string]any); ok {
			delete(labels, podTemplateHashLabel)
			if len(labels) == 0 {
				delete(templateMeta, "labels")
			}
		}
	}

	depAnnotations, _, _ := unstructured.NestedStringMap(deployment.Object, "metadata", "annotations")
	rsAnnotations, _, _ := unstructured.NestedStringMap(rs.Object, "metadata", "annotations")

	merged := map[string]string{}
	for k := range deploymentRollbackAnnotationsToSkip {
		if v, ok := depAnnotations[k]; ok {
			merged[k] = v
		}
	}
	for k, v := range rsAnnotations {
		if !deploymentRollbackAnnotationsToSkip[k] {
			merged[k] = v
		}
	}

	return json.Marshal([]any{
		map[string]any{"op": "replace", "path": "/spec/template", "value": template},
		map[string]any{"op": "replace", "path": "/metadata/annotations", "value": merged},
	})
}

// undoControllerRevisionRollout implements rollback for apps/v1 StatefulSets
// and DaemonSets, both of which use ControllerRevision objects to keep history.
//
// The ControllerRevision's data field is itself a strategic-merge patch
// fragment scoped at spec (with $patch: replace under spec.template), so we
// re-send it verbatim — exactly what `kubectl rollout undo` does.
func (m *Manager) undoControllerRevisionRollout(
	ctx context.Context,
	client *kubernetes.Client,
	gvr schema.GroupVersionResource,
	namespace, name string,
	toRevision int64,
) (*mcp.CallToolResult, error) {

	parent, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	parentUID := nestedString(parent.Object, "metadata", "uid")

	// status.currentRevision holds the NAME of the ControllerRevision the
	// controller currently has rolled out (a string, NOT a number). We use it
	// to identify the current revision number in the history below; without
	// this, after any prior undo the highest revision in history would be a
	// future state and "rollback to N-1" would resolve incorrectly.
	currentRevisionName := nestedString(parent.Object, "status", "currentRevision")

	crGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "controllerrevisions"}
	crList, err := client.DynamicClient.Resource(crGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	type crRevision struct {
		revision int64
		obj      *unstructured.Unstructured
	}

	var history []crRevision
	var currentRevision int64
	for i := range crList.Items {
		item := &crList.Items[i]
		if !ownedBy(item.Object, parentUID) {
			continue
		}
		rev, _, err := unstructured.NestedInt64(item.Object, "revision")
		if err != nil || rev == 0 {
			continue
		}
		history = append(history, crRevision{revision: rev, obj: item})
		if currentRevisionName != "" && item.GetName() == currentRevisionName {
			currentRevision = rev
		}
	}

	if len(history) == 0 {
		return errorResult(fmt.Errorf("no rollout history found for %s %s/%s", gvr.Resource, namespace, name)), nil
	}

	sort.Slice(history, func(i, j int) bool { return history[i].revision > history[j].revision })

	// If the parent never reported status.currentRevision (e.g. brand new
	// resource not yet observed by the controller, or a non-standard
	// implementation), fall back to "highest revision is current". This
	// matches the previous behaviour and is safe for the common case.
	if currentRevision == 0 {
		currentRevision = history[0].revision
	}

	target, err := pickTargetRevision(history, currentRevision, toRevision, func(r crRevision) int64 { return r.revision })
	if err != nil {
		return errorResult(err), nil
	}

	// ControllerRevision stores the patch / serialized spec under data.raw.
	// The patch is a strategic-merge patch fragment that applied on top of the
	// (empty/initial) parent spec yields the historical pod template.
	rawData, found, err := unstructured.NestedFieldCopy(target.obj.Object, "data")
	if err != nil || !found {
		return errorResult(fmt.Errorf("target revision %d for %s %s/%s has no data field", target.revision, gvr.Resource, namespace, name)), nil
	}

	patchBytes, err := json.Marshal(rawData)
	if err != nil {
		return errorResult(err), nil
	}

	if _, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}); err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully rolled back %s %s/%s to revision %d", gvr.Resource, namespace, name, target.revision)), nil
}

// pickTargetRevision chooses the revision to roll back to.
// 'history' must already be sorted by revision DESCENDING.
//
//   - When 'toRevision' > 0: that exact revision must exist and must NOT be the
//     current one.
//   - When 'toRevision' == 0: pick the highest revision strictly lower than
//     'currentRevision' (kubectl semantics — N-1, not the latest in the list).
func pickTargetRevision[T any](history []T, currentRevision, toRevision int64, revOf func(T) int64) (T, error) {
	var zero T

	if toRevision > 0 {
		if toRevision == currentRevision {
			return zero, fmt.Errorf("revision %d is already the current one; nothing to do", toRevision)
		}
		for _, h := range history {
			if revOf(h) == toRevision {
				return h, nil
			}
		}
		return zero, fmt.Errorf("revision %d not found in rollout history", toRevision)
	}

	// Default: previous (N-1).
	for _, h := range history {
		if revOf(h) < currentRevision {
			return h, nil
		}
	}
	return zero, fmt.Errorf("no previous revision available to roll back to")
}

// ownedBy reports whether obj has an ownerReference with the given UID.
func ownedBy(obj map[string]any, ownerUID string) bool {
	if ownerUID == "" {
		return false
	}
	refs, _, _ := unstructured.NestedSlice(obj, "metadata", "ownerReferences")
	for _, r := range refs {
		ref, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if uid, _ := ref["uid"].(string); uid == ownerUID {
			return true
		}
	}
	return false
}

// nestedString is a small typed wrapper around unstructured.NestedString that
// returns "" instead of an error/unset, to keep call sites readable.
func nestedString(obj map[string]any, fields ...string) string {
	v, _, _ := unstructured.NestedString(obj, fields...)
	return v
}

// Helper to extract nested values
