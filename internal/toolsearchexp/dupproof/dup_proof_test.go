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

// Package dupproof shows that a toolset which both returns a tool from
// Tools() and packs it again in ProcessRequest aborts the run: the framework
// already packs every tool Tools() returned, so the second PackTool collides.
package dupproof

import (
	"context"
	"iter"
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

type textLLM struct{}

func (textLLM) Name() string { return "text-llm" }

func (textLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "ok"}}},
			TurnComplete: true,
		}, nil)
	}
}

type emptyArgs struct{}

type textResult struct {
	Text string `json:"text"`
}

// doublePacker exposes search_tools from Tools() AND packs it in
// ProcessRequest, the mistake a lazy-catalog wrapper naturally makes.
type doublePacker struct{ search tool.Tool }

func (d *doublePacker) Name() string { return "double" }

func (d *doublePacker) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return []tool.Tool{d.search}, nil
}

func (d *doublePacker) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	return toolutils.PackTool(req, d.search.(interface {
		Name() string
		Declaration() *genai.FunctionDeclaration
	}))
}

func TestPackingABootstrapToolTwiceAbortsTheRun(t *testing.T) {
	search, err := functiontool.New[emptyArgs, textResult](
		functiontool.Config{Name: "search_tools", Description: "Find tools."},
		func(ctx agent.Context, _ emptyArgs) (textResult, error) { return textResult{}, nil })
	if err != nil {
		t.Fatalf("functiontool.New() = %v", err)
	}
	a, err := llmagent.New(llmagent.Config{
		Name:     "dup_agent",
		Model:    textLLM{},
		Toolsets: []tool.Toolset{&doublePacker{search: search}},
	})
	if err != nil {
		t.Fatalf("llmagent.New() = %v", err)
	}
	r, err := runner.New(runner.Config{
		AppName:           "dupproof",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		t.Fatalf("runner.New() = %v", err)
	}

	var errs []string
	msg := genai.NewContentFromText("go", genai.RoleUser)
	for _, err := range r.Run(t.Context(), "u", "s", msg, agent.RunConfig{}) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	all := strings.Join(errs, " | ")
	t.Logf("run errors: %s", all)
	if !strings.Contains(all, `duplicate tool: "search_tools"`) {
		t.Errorf("run errors = %q, want a duplicate-tool failure for search_tools", all)
	}
}
