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

// Package lazyclean measures a lazy tool catalog written the "clean" way:
// everything expressed through Toolset.Tools(ctx), with no request-processor
// side channel. Run it with and without PR #785 to see what the fix buys.
package lazyclean

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

const discoveredKey = "lazycatalog:discovered"

type recordingLLM struct {
	script []*genai.Content
	seen   [][]string
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
			yield(nil, fmt.Errorf("model called %d times, script has %d", call+1, len(m.script)))
			return
		}
		yield(&model.LLMResponse{Content: m.script[call], TurnComplete: true}, nil)
	}
}

// cleanLazyToolset expresses the whole lazy catalog through Tools(ctx).
// No ProcessRequest, no side channel. This is the shape the design would
// have if Tools() were re-evaluated every step.
type cleanLazyToolset struct {
	searchTool tool.Tool
	byName     map[string]tool.Tool
	calls      int // how many times Tools() was invoked
}

func (ts *cleanLazyToolset) Name() string { return "clean_lazy" }

func (ts *cleanLazyToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	ts.calls++
	out := []tool.Tool{ts.searchTool}
	v, err := ctx.ReadonlyState().Get(discoveredKey)
	if err != nil {
		return out, nil
	}
	names, _ := v.([]any)
	for _, n := range names {
		if t, ok := ts.byName[n.(string)]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

type emptyArgs struct{}

type textResult struct {
	Text string `json:"text"`
}

func build(t *testing.T, m model.LLM) (agent.Agent, *cleanLazyToolset) {
	t.Helper()

	searchTool, err := functiontool.New[emptyArgs, textResult](
		functiontool.Config{Name: "search_tools", Description: "Find tools by query."},
		func(ctx agent.Context, _ emptyArgs) (textResult, error) {
			// Append-only discovery list, exactly as the design doc describes.
			var names []any
			if v, err := ctx.State().Get(discoveredKey); err == nil {
				names, _ = v.([]any)
			}
			names = append(names, "secret_tool")
			if err := ctx.State().Set(discoveredKey, names); err != nil {
				return textResult{}, err
			}
			return textResult{Text: "revealed secret_tool"}, nil
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

	ts := &cleanLazyToolset{
		searchTool: searchTool,
		byName:     map[string]tool.Tool{"secret_tool": secretTool},
	}
	a, err := llmagent.New(llmagent.Config{
		Name:     "lazy_agent",
		Model:    m,
		Toolsets: []tool.Toolset{ts},
	})
	if err != nil {
		t.Fatalf("llmagent.New() = %v", err)
	}
	return a, ts
}

type funcResponse struct {
	name     string
	response map[string]any
}

func run(t *testing.T, a agent.Agent) ([]funcResponse, []error) {
	t.Helper()
	r, err := runner.New(runner.Config{
		AppName:           "lazyclean",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		t.Fatalf("runner.New() = %v", err)
	}
	var errs []error
	var out []funcResponse
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
				out = append(out, funcResponse{p.FunctionResponse.Name, p.FunctionResponse.Response})
			}
		}
	}
	return out, errs
}

func TestCleanLazyCatalog(t *testing.T) {
	m := &recordingLLM{script: []*genai.Content{
		{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "search_tools", Args: map[string]any{}}}}},
		{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "secret_tool", Args: map[string]any{}}}}},
		{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "done"}}},
	}}
	a, ts := build(t, m)
	responses, errs := run(t, a)

	for i, got := range m.seen {
		t.Logf("generation %d declarations: %v", i+1, got)
	}
	for _, r := range responses {
		t.Logf("function response %q -> %v", r.name, r.response)
	}
	t.Logf("RESULT generations=%d toolsCalls=%d errs=%v", len(m.seen), ts.calls, errs)

	secretOK := false
	for _, r := range responses {
		if r.name == "secret_tool" {
			if _, isErr := r.response["error"]; !isErr {
				secretOK = true
			}
		}
	}
	t.Logf("RESULT secret_tool executed successfully: %v", secretOK)
	if !secretOK {
		t.Errorf("secret_tool did not run after being discovered; Toolset.Tools() is not re-evaluated per model step (google/adk-go#757)")
	}
}
