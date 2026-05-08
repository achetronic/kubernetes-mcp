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
	tool := mcp.NewTool(m.toolName("get_logs"),
		mcp.WithDescription(`Retrieve container logs from a Pod.

Always combine 'tail_lines' or 'since_seconds' with this tool unless you are
sure the log volume is small. A chatty container can return megabytes per
second, which the model is not the right place to handle.

For multi-container Pods you must set 'container'. To inspect logs from a
crashed container that has been restarted, set 'previous: true'.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the Pod whose logs to fetch.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the Pod lives. Defaults to 'default' if empty.")),
		mcp.WithString("container", mcp.Description("Name of the container inside the Pod. Required when the Pod has more than one container; ignored otherwise.")),
		mcp.WithBoolean("previous", mcp.Description("If true, return logs from the previous instance of the container (i.e. before the last restart). Useful to investigate crash loops. Fails if the container has never restarted.")),
		mcp.WithNumber("since_seconds", mcp.Description("Only return logs newer than this many seconds. Integer >= 1. Omit or 0 to disable.")),
		mcp.WithNumber("tail_lines", mcp.Description("Return only the last N lines. Integer >= 1. Omit or 0 to return all logs (potentially huge).")),
		mcp.WithBoolean("timestamps", mcp.Description("If true, prepend an RFC3339 timestamp to each line. Default false.")),
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
		Group:    "",
		Version:  "v1",
		Resource: "pods",
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
	tool := mcp.NewTool(m.toolName("exec_command"),
		mcp.WithDescription(`Run a one-shot, non-interactive command inside a running container and
return its stdout and stderr.

Constraints:
  - Non-interactive (no TTY, no stdin). Anything that requires user input
    or paging will block until timeout.
  - Hard timeout of 30 seconds per call. Use long-running diagnostics in
    smaller, scoped commands.
  - The container must already exist (Pod in Running phase).

Typical uses: 'cat /etc/config.yaml', 'env', 'ps aux', 'ls /var/log'.
Avoid 'top', 'tail -f', 'sh' and similar interactive sessions.`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Name of the Pod to exec into.")),
		mcp.WithString("namespace", mcp.Description("Namespace where the Pod lives. Defaults to 'default' if empty.")),
		mcp.WithString("container", mcp.Description("Name of the container inside the Pod. Required when the Pod has more than one container.")),
		mcp.WithArray("command", mcp.Required(), mcp.Description("Command and arguments as an array of strings. Example: [\"ls\", \"-la\", \"/var/log\"]. Use shell features by wrapping in 'sh -c': [\"sh\", \"-c\", \"echo $HOSTNAME && date\"].")),
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
		Group:    "",
		Version:  "v1",
		Resource: "pods",
		Name:     name,
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
	tool := mcp.NewTool(m.toolName("list_events"),
		mcp.WithDescription(`List Kubernetes events from a namespace or the whole cluster.

Best when investigating recent failures: scheduling, pulling images, OOM
kills, probe failures, etc. Events are time-bounded (the API server prunes
them after a few hours by default), so don't rely on them for audit.

Combine 'field_selector' and 'types' to narrow the noise. Use 'yq_expressions'
to project just the fields you care about ('reason', 'message', 'involvedObject').`),
		mcp.WithString("context", mcp.Description("Kubernetes context to target. If empty, uses the currently active MCP context.")),
		mcp.WithString("namespace", mcp.Description("Namespace to scope the listing to. Empty lists across ALL namespaces (subject to RBAC).")),
		mcp.WithString("field_selector", mcp.Description("Field selector. Common keys: 'involvedObject.name', 'involvedObject.kind', 'involvedObject.namespace', 'reason', 'type'. Example: 'involvedObject.name=my-pod,type=Warning'.")),
		mcp.WithArray("types", mcp.Description("Filter by event type. Accepts an array containing any of: 'Normal', 'Warning'. Empty or omitted means no type filter.")),
		mcp.WithArray("yq_expressions", mcp.Description("Optional yq expressions applied to the events list. The output is an EventList so use '.items[]' to iterate. Examples: '.items[] | select(.type == \"Warning\") | .message' (all warning messages), '.items[] | {when: .lastTimestamp, reason: .reason, msg: .message}' (compact view).")),
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
		Group:    "",
		Version:  "v1",
		Resource: "events",
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
