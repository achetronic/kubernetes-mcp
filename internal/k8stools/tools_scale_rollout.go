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
	"time"

	"kubernetes-mcp/internal/authorization"

	"github.com/mark3labs/mcp-go/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func (m *Manager) registerScaleResource() {
	tool := mcp.NewTool("scale_resource",
		mcp.WithDescription("Scales a Deployment, ReplicaSet, or StatefulSet"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group (default: apps)")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithNumber("replicas", mcp.Required(), mcp.Description("Desired number of replicas")),
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
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	replicas, _ := args["replicas"].(float64)

	// Check authorization
	if err := m.checkAuthorization(request, "scale_resource", k8sContext, namespace, authorization.ResourceInfo{
		Group:   group,
		Version: version,
		Kind:    kind,
		Name:    name,
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

	gvr := getGVR(group, version, kind)

	result, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	yamlOutput, err := objectToYAML(result)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully scaled %s/%s to %d replicas\n\n%s", kind, name, int(replicas), yamlOutput)), nil
}

func (m *Manager) registerGetRolloutStatus() {
	tool := mcp.NewTool("get_rollout_status",
		mcp.WithDescription("Gets the status of a rollout"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group (default: apps)")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind (Deployment, DaemonSet, StatefulSet)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
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
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "get_rollout_status", k8sContext, namespace, authorization.ResourceInfo{
		Group:   group,
		Version: version,
		Kind:    kind,
		Name:    name,
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

	gvr := getGVR(group, version, kind)

	obj, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	// Extract relevant status information
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")

	desiredReplicas, _, _ := unstructured.NestedInt64(spec, "replicas")
	readyReplicas, _, _ := unstructured.NestedInt64(status, "readyReplicas")
	updatedReplicas, _, _ := unstructured.NestedInt64(status, "updatedReplicas")
	availableReplicas, _, _ := unstructured.NestedInt64(status, "availableReplicas")
	observedGeneration, _, _ := unstructured.NestedInt64(status, "observedGeneration")
	generation := obj.GetGeneration()

	statusText := fmt.Sprintf(`Rollout Status for %s/%s:
  Desired:    %d
  Ready:      %d
  Updated:    %d
  Available:  %d
  Generation: %d (observed: %d)
  Synced:     %v`,
		kind, name,
		desiredReplicas,
		readyReplicas,
		updatedReplicas,
		availableReplicas,
		generation,
		observedGeneration,
		generation == observedGeneration,
	)

	// Check conditions
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

	return successResult(statusText), nil
}

func (m *Manager) registerRestartRollout() {
	tool := mcp.NewTool("restart_rollout",
		mcp.WithDescription("Restarts a rollout by updating the restart annotation"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group (default: apps)")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind (Deployment, DaemonSet, StatefulSet)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
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
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)

	// Check authorization
	if err := m.checkAuthorization(request, "restart_rollout", k8sContext, namespace, authorization.ResourceInfo{
		Group:   group,
		Version: version,
		Kind:    kind,
		Name:    name,
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

	gvr := getGVR(group, version, kind)

	_, err = client.DynamicClient.Resource(gvr).Namespace(namespace).Patch(
		ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(fmt.Sprintf("Successfully triggered restart for %s/%s", kind, name)), nil
}

func (m *Manager) registerUndoRollout() {
	tool := mcp.NewTool("undo_rollout",
		mcp.WithDescription("Reverts a rollout to a previous revision"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("group", mcp.Description("API group (default: apps)")),
		mcp.WithString("version", mcp.Required(), mcp.Description("API version")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind (Deployment, DaemonSet, StatefulSet)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithNumber("to_revision", mcp.Description("Revision to rollback to (default: previous revision)")),
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
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	toRevision, _ := args["to_revision"].(float64)

	// Check authorization
	if err := m.checkAuthorization(request, "undo_rollout", k8sContext, namespace, authorization.ResourceInfo{
		Group:   group,
		Version: version,
		Kind:    kind,
		Name:    name,
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

	// For Deployments, we need to find the ReplicaSet and patch it
	// This is a simplified implementation - kubectl does more sophisticated handling
	switch kind {
	case "Deployment":
		// Get the deployment
		gvr := getGVR(group, version, kind)
		deployment, err := client.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return errorResult(err), nil
		}

		// Find ReplicaSets for this deployment
		rsGVR := getGVR("apps", "v1", "ReplicaSet")
		selector, _, _ := unstructured.NestedString(deployment.Object, "spec", "selector", "matchLabels")
		_ = selector // Use this to find matching ReplicaSets

		rsList, err := client.DynamicClient.Resource(rsGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return errorResult(err), nil
		}

		// Find the ReplicaSet with the desired revision
		var targetRS *unstructured.Unstructured
		for _, item := range rsList.Items {
			// Check owner references
			ownerRefs, _, _ := unstructured.NestedSlice(item.Object, "metadata", "ownerReferences")
			for _, ref := range ownerRefs {
				if refMap, ok := ref.(map[string]any); ok {
					if refName, _ := refMap["name"].(string); refName == name {
						// Check revision annotation
						annotations, _, _ := unstructured.NestedMap(item.Object, "metadata", "annotations")
						if revision, ok := annotations["deployment.kubernetes.io/revision"].(string); ok {
							if toRevision > 0 && revision == fmt.Sprintf("%d", int(toRevision)) {
								targetRS = &item
								break
							} else if toRevision == 0 && targetRS == nil {
								// Keep track of the latest RS for rollback
								targetRS = &item
							}
						}
					}
				}
			}
		}

		if targetRS == nil {
			return errorResult(fmt.Errorf("no suitable revision found for rollback")), nil
		}

		// Get the pod template from the target ReplicaSet
		template, _, _ := unstructured.NestedMap(targetRS.Object, "spec", "template")

		// Patch the deployment with the template from the target RS
		patch := map[string]any{
			"spec": map[string]any{
				"template": template,
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

		return successResult(fmt.Sprintf("Successfully rolled back %s/%s", kind, name)), nil

	default:
		return errorResult(fmt.Errorf("undo rollout is only supported for Deployments")), nil
	}
}

// Helper to extract nested values
func unstructured_NestedMap(obj map[string]any, fields ...string) (map[string]any, bool, error) {
	return unstructured.NestedMap(obj, fields...)
}
