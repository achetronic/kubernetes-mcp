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

package yqutil

import (
	"fmt"
	"strings"

	"github.com/mikefarah/yq/v4/pkg/yqlib"
	"gopkg.in/op/go-logging.v1"
)

func init() {
	// Disable yq logging
	logging.SetLevel(logging.ERROR, "yq-lib")
}

// Evaluator processes yq expressions on YAML data
type Evaluator struct{}

// NewEvaluator creates a new yq evaluator
func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// Evaluate applies yq expressions in cascade to the input YAML
func (e *Evaluator) Evaluate(input string, expressions []string) (string, error) {
	if len(expressions) == 0 {
		return input, nil
	}

	current := input
	for _, expr := range expressions {
		result, err := e.evaluateSingle(current, expr)
		if err != nil {
			return "", fmt.Errorf("failed to evaluate expression %q: %w", expr, err)
		}
		current = result
	}

	return strings.TrimSpace(current), nil
}

// evaluateSingle applies a single yq expression to the input
func (e *Evaluator) evaluateSingle(input string, expression string) (string, error) {
	// Parse the input YAML into candidate nodes
	decoder := yqlib.NewYamlDecoder(yqlib.YamlPreferences{
		EvaluateTogether: false,
	})

	reader := strings.NewReader(input)
	documents, err := yqlib.ReadDocuments(reader, decoder)
	if err != nil {
		return "", fmt.Errorf("failed to parse YAML: %w", err)
	}

	if documents.Len() == 0 {
		return "", nil
	}

	// Evaluate the expression
	evaluator := yqlib.NewAllAtOnceEvaluator()

	// Convert list to candidate nodes
	var nodes []*yqlib.CandidateNode
	for el := documents.Front(); el != nil; el = el.Next() {
		if node, ok := el.Value.(*yqlib.CandidateNode); ok {
			nodes = append(nodes, node)
		}
	}

	results, err := evaluator.EvaluateNodes(expression, nodes...)
	if err != nil {
		return "", fmt.Errorf("failed to evaluate expression: %w", err)
	}

	// Encode results back to YAML
	var output strings.Builder
	encoder := yqlib.NewYamlEncoder(yqlib.YamlPreferences{
		Indent:             2,
		ColorsEnabled:      false,
		PrintDocSeparators: false,
		UnwrapScalar:       true,
	})

	for el := results.Front(); el != nil; el = el.Next() {
		if node, ok := el.Value.(*yqlib.CandidateNode); ok {
			err := encoder.Encode(&output, node)
			if err != nil {
				return "", fmt.Errorf("failed to encode result: %w", err)
			}
		}
	}

	return output.String(), nil
}

// EvaluateToStrings evaluates and returns results as a slice of strings (for list results)
func (e *Evaluator) EvaluateToStrings(input string, expression string) ([]string, error) {
	result, err := e.evaluateSingle(input, expression)
	if err != nil {
		return nil, err
	}

	// Split by newlines for multiple results
	lines := strings.Split(strings.TrimSpace(result), "\n")
	var filtered []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && trimmed != "---" {
			filtered = append(filtered, trimmed)
		}
	}

	return filtered, nil
}
