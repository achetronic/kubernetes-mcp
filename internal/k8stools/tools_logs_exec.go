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
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"kubernetes-mcp/internal/authorization"

	"github.com/mark3labs/mcp-go/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

func (m *Manager) registerGetLogs() {
	tool := mcp.NewTool("get_logs",
		mcp.WithDescription("Gets logs from a Pod"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Pod name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithString("container", mcp.Description("Container name (required if pod has multiple containers)")),
		mcp.WithBoolean("previous", mcp.Description("Get logs from the previous container instance")),
		mcp.WithNumber("since_seconds", mcp.Description("Only return logs newer than this many seconds")),
		mcp.WithNumber("tail_lines", mcp.Description("Number of lines from the end of the logs to show")),
		mcp.WithBoolean("timestamps", mcp.Description("Include timestamps in the log output")),
	)
	m.mcpServer.AddTool(tool, m.handleGetLogs)
}

func (m *Manager) handleGetLogs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	if namespace == "" {
		namespace = "default"
	}
	container, _ := args["container"].(string)
	previous, _ := args["previous"].(bool)
	sinceSeconds, _ := args["since_seconds"].(float64)
	tailLines, _ := args["tail_lines"].(float64)
	timestamps, _ := args["timestamps"].(bool)

	// Check authorization (real K8s resource: Pod)
	if err := m.checkAuthorization(request, "get_logs", k8sContext, namespace, authorization.ResourceInfo{
		Group:   "",
		Version: "v1",
		Kind:    "Pod",
		Name:    name,
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

	opts := &corev1.PodLogOptions{
		Container:  container,
		Previous:   previous,
		Timestamps: timestamps,
	}

	if sinceSeconds > 0 {
		since := int64(sinceSeconds)
		opts.SinceSeconds = &since
	}

	if tailLines > 0 {
		tail := int64(tailLines)
		opts.TailLines = &tail
	}

	req := client.Clientset.CoreV1().Pods(namespace).GetLogs(name, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return errorResult(err), nil
	}
	defer stream.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, stream)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(buf.String()), nil
}

func (m *Manager) registerExecCommand() {
	tool := mcp.NewTool("exec_command",
		mcp.WithDescription("Executes a command in a container (non-interactive)"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Pod name")),
		mcp.WithString("namespace", mcp.Description("Namespace")),
		mcp.WithString("container", mcp.Description("Container name")),
		mcp.WithArray("command", mcp.Required(), mcp.Description("Command to execute as array of strings")),
	)
	m.mcpServer.AddTool(tool, m.handleExecCommand)
}

func (m *Manager) handleExecCommand(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	name, _ := args["name"].(string)
	namespace, _ := args["namespace"].(string)
	if namespace == "" {
		namespace = "default"
	}
	container, _ := args["container"].(string)
	commandArg, _ := args["command"].([]any)

	// Check authorization (real K8s resource: Pod)
	if err := m.checkAuthorization(request, "exec_command", k8sContext, namespace, authorization.ResourceInfo{
		Group:   "",
		Version: "v1",
		Kind:    "Pod",
		Name:    name,
	}); err != nil {
		return errorResult(err), nil
	}

	if !m.clientManager.IsNamespaceAllowed(k8sContext, namespace) {
		return errorResult(fmt.Errorf("namespace %s is not allowed in context %s", namespace, k8sContext)), nil
	}

	var command []string
	for _, c := range commandArg {
		if s, ok := c.(string); ok {
			command = append(command, s)
		}
	}

	if len(command) == 0 {
		return errorResult(fmt.Errorf("command is required")), nil
	}

	client, err := m.clientManager.GetClient(k8sContext)
	if err != nil {
		return errorResult(err), nil
	}

	req := client.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(name).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   command,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(client.Config, "POST", req.URL())
	if err != nil {
		return errorResult(err), nil
	}

	var stdout, stderr bytes.Buffer

	// Use a timeout context
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n--- stderr ---\n" + stderr.String()
	}

	if err != nil {
		return successResult(fmt.Sprintf("Command exited with error: %v\n\nOutput:\n%s", err, output)), nil
	}

	return successResult(output), nil
}

func (m *Manager) registerListEvents() {
	tool := mcp.NewTool("list_events",
		mcp.WithDescription("Lists cluster or namespace events"),
		mcp.WithString("context", mcp.Description("Kubernetes context to use")),
		mcp.WithString("namespace", mcp.Description("Namespace (empty for all namespaces)")),
		mcp.WithString("field_selector", mcp.Description("Field selector (e.g., 'involvedObject.name=my-pod')")),
		mcp.WithArray("types", mcp.Description("Event types to filter: 'Normal', 'Warning'")),
		mcp.WithArray("yq_expressions", mcp.Description("Array of yq expressions (https://mikefarah.gitbook.io/yq) to filter/transform the YAML output. Applied sequentially. Examples: '.items[] | select(.type == \"Warning\")' (filter warnings), '.items[].message' (get all messages), '.items[] | {name: .involvedObject.name, reason: .reason}' (reshape)")),
	)
	m.mcpServer.AddTool(tool, m.handleListEvents)
}

func (m *Manager) handleListEvents(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	k8sContext := m.getContextParam(args)
	namespace, _ := args["namespace"].(string)
	fieldSelector, _ := args["field_selector"].(string)
	eventTypes, _ := args["types"].([]any)

	// Check authorization (real K8s resource: Event)
	if err := m.checkAuthorization(request, "list_events", k8sContext, namespace, authorization.ResourceInfo{
		Group:   "",
		Version: "v1",
		Kind:    "Event",
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

	listOpts := metav1.ListOptions{
		FieldSelector: fieldSelector,
	}

	var events *corev1.EventList
	if namespace != "" {
		events, err = client.Clientset.CoreV1().Events(namespace).List(ctx, listOpts)
	} else {
		events, err = client.Clientset.CoreV1().Events("").List(ctx, listOpts)
	}

	if err != nil {
		return errorResult(err), nil
	}

	// Filter by event types if specified
	if len(eventTypes) > 0 {
		var typeFilter []string
		for _, t := range eventTypes {
			if s, ok := t.(string); ok {
				typeFilter = append(typeFilter, s)
			}
		}

		var filteredItems []corev1.Event
		for _, event := range events.Items {
			for _, t := range typeFilter {
				if strings.EqualFold(event.Type, t) {
					filteredItems = append(filteredItems, event)
					break
				}
			}
		}
		events.Items = filteredItems
	}

	yamlOutput, err := objectToYAML(events)
	if err != nil {
		return errorResult(err), nil
	}

	// Apply yq expressions
	finalOutput, err := m.applyYQExpressions(yamlOutput, args)
	if err != nil {
		return errorResult(err), nil
	}

	return successResult(finalOutput), nil
}
