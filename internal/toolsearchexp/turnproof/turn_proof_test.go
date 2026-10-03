// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package turnproof answers one question empirically: with a lazy tool
// catalog built the way the design doc describes (a search tool records
// discoveries in session state, a toolset request processor packs the
// discovered declarations on a later step), can the model call a discovered
// tool in the SAME generation that discovered it?
package turnproof

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/toolutils"
)

const discoveredKey = "lazycatalog:discovered"

// recordingLLM records the function declarations visible in every request and
// replays a scripted response per call.
type recordingLLM struct {
	script []*genai.Content
	// seen[i] holds the sorted declaration names present in request i.
	seen [][]string
}

func (m *recordingLLM) Name() string { return "recording-llm" }

func (m *recordingLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var names []string
		if req.Config != nil {
			for _, t := range req.Config.Tools {
				if t == nil {
					continue
				}
				for _, fd := range t.FunctionDeclarations {
					names = append(names, fd.Name)
				}
			}
		}
		slices.Sort(names)
		call := len(m.seen)
		m.seen = append(m.seen, names)

		if call >= len(m.script) {
			yield(nil, fmt.Errorf("model called %d times, script has %d entries", call+1, len(m.script)))
			return
		}
		yield(&model.LLMResponse{Content: m.script[call], TurnComplete: true}, nil)
	}
}

func fcContent(name string) *genai.Content {
	return &genai.Content{
		Role:  genai.RoleModel,
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: name, Args: map[string]any{}}}},
	}
}

func textContent(s string) *genai.Content {
	return &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: s}}}
}

// lazyToolset is the design doc's shape: Tools() exposes only the bootstrap
// set, and ProcessRequest packs discovered tools on every later model step.
type lazyToolset struct {
	searchTool tool.Tool
	secretTool tool.Tool
}

func (ts *lazyToolset) Name() string { return "lazy" }

func (ts *lazyToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return []tool.Tool{ts.searchTool}, nil
}

// ProcessRequest runs on every model step (toolsetPreprocess), so it is the
// hook the design relies on to reveal discovered tools.
func (ts *lazyToolset) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	if v, err := ctx.State().Get(discoveredKey); err == nil && v == true {
		return toolutils.PackTool(req, ts.secretTool.(interface {
			Name() string
			Declaration() *genai.FunctionDeclaration
		}))
	}
	return nil
}

type emptyArgs struct{}

type textResult struct {
	Text string `json:"text"`
}

func newAgent(t *testing.T, m model.LLM) agent.Agent {
	t.Helper()

	searchTool, err := functiontool.New[emptyArgs, textResult](
		functiontool.Config{Name: "search_tools", Description: "Find tools by query."},
		func(ctx agent.Context, _ emptyArgs) (textResult, error) {
			if err := ctx.State().Set(discoveredKey, true); err != nil {
				return textResult{}, err
			}
			return textResult{Text: "found secret_tool; continue on the next step"}, nil
		})
	if err != nil {
		t.Fatalf("functiontool.New(search_tools) = %v", err)
	}

	secretTool, err := functiontool.New[emptyArgs, textResult](
		functiontool.Config{Name: "secret_tool", Description: "The deferred tool."},
		func(ctx agent.Context, _ emptyArgs) (textResult, error) {
			return textResult{Text: "secret_tool ran"}, nil
		})
	if err != nil {
		t.Fatalf("functiontool.New(secret_tool) = %v", err)
	}

	a, err := llmagent.New(llmagent.Config{
		Name:     "lazy_agent",
		Model:    m,
		Toolsets: []tool.Toolset{&lazyToolset{searchTool: searchTool, secretTool: secretTool}},
	})
	if err != nil {
		t.Fatalf("llmagent.New() = %v", err)
	}
	return a
}

// funcResponse is one function result observed on the event stream.
type funcResponse struct {
	name     string
	response map[string]any
}

func run(t *testing.T, a agent.Agent) ([]funcResponse, []error) {
	t.Helper()
	r, err := runner.New(runner.Config{
		AppName:           "turnproof",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		t.Fatalf("runner.New() = %v", err)
	}
	var errs []error
	var responses []funcResponse
	msg := genai.NewContentFromText("go", genai.RoleUser)
	for ev, err := range r.Run(t.Context(), "u", "s", msg, agent.RunConfig{}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ev == nil || ev.LLMResponse.Content == nil {
			continue
		}
		for _, p := range ev.LLMResponse.Content.Parts {
			if p.FunctionResponse != nil {
				responses = append(responses, funcResponse{
					name:     p.FunctionResponse.Name,
					response: p.FunctionResponse.Response,
				})
			}
		}
	}
	return responses, errs
}

// TestDiscoveredToolIsAbsentFromTheGenerationThatDiscoversIt shows the
// declaration timeline: secret_tool cannot be in the request that produced the
// search call, because that request was built and sent before the tool ran.
func TestDiscoveredToolIsAbsentFromTheGenerationThatDiscoversIt(t *testing.T) {
	m := &recordingLLM{script: []*genai.Content{
		fcContent("search_tools"), // generation 1
		fcContent("secret_tool"),  // generation 2
		textContent("done"),       // generation 3
	}}
	if _, errs := run(t, newAgent(t, m)); len(errs) != 0 {
		t.Fatalf("run errors = %v, want none", errs)
	}

	if len(m.seen) != 3 {
		t.Fatalf("model calls = %d, want 3; seen = %v", len(m.seen), m.seen)
	}
	for i, got := range m.seen {
		t.Logf("generation %d saw declarations: %v", i+1, got)
	}
	if slices.Contains(m.seen[0], "secret_tool") {
		t.Errorf("generation 1 saw secret_tool; want it absent (declarations: %v)", m.seen[0])
	}
	if !slices.Contains(m.seen[1], "secret_tool") {
		t.Errorf("generation 2 did not see secret_tool; want it present (declarations: %v)", m.seen[1])
	}
}

// TestCallingADiscoveredToolInTheSameGenerationFails is the load-bearing one.
// It is not enough that the model "would not" call an undeclared tool. Here the
// model DOES emit both calls in one generation, and the framework rejects the
// second, proving the extra model turn is structural and not a convention.
func TestCallingADiscoveredToolInTheSameGenerationFails(t *testing.T) {
	both := &genai.Content{
		Role: genai.RoleModel,
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: "search_tools", Args: map[string]any{}}},
			{FunctionCall: &genai.FunctionCall{Name: "secret_tool", Args: map[string]any{}}},
		},
	}
	m := &recordingLLM{script: []*genai.Content{both, textContent("done")}}

	responses, errs := run(t, newAgent(t, m))
	if len(errs) != 0 {
		t.Fatalf("run errors = %v, want none", errs)
	}
	t.Logf("generation 1 saw declarations: %v", m.seen[0])
	for _, r := range responses {
		t.Logf("function response %q -> %v", r.name, r.response)
	}

	var secret *funcResponse
	for i, r := range responses {
		if r.name == "secret_tool" {
			secret = &responses[i]
		}
	}
	if secret == nil {
		t.Fatalf("no function response for secret_tool; responses = %v", responses)
	}
	errText, _ := secret.response["error"].(string)
	if !strings.Contains(errText, "not found") {
		t.Errorf("secret_tool executed in the discovering generation; response = %v, want a not-found error", secret.response)
	}
	// It resolved on a later generation only because the model was asked again.
	if len(m.seen) < 2 {
		t.Errorf("model calls = %d, want at least 2", len(m.seen))
	}
}
