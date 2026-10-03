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

// Package presearch measures how many model generations three tool-exposure
// strategies need to reach the same tool call:
//
//	A eager     - every tool declared up front (the no-lazy baseline)
//	B searchTool - the design doc's shape: the model calls search_tools first
//	C preSearch  - the toolset searches from ctx.UserContent() before generation 1
//
// The model is reactive rather than scripted: it calls the task tool when the
// task tool is declared, falls back to search_tools when it is not, and
// otherwise finishes. So the generation count is measured, not assumed.
package presearch

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
)

const (
	taskTool      = "send_invoice_email"
	discoveredKey = "lazycatalog:discovered"
	userQuery     = "send an invoice email to the customer"
	// missQuery shares no vocabulary with any catalog entry.
	missQuery = "sort out the billing paperwork for this account"
)

// catalog is the deferred set. Only taskTool answers userQuery.
var catalog = map[string]string{
	taskTool:            "Send an invoice email to a customer.",
	"restart_vm":        "Restart a virtual machine instance.",
	"query_warehouse":   "Run a SQL query against the data warehouse.",
	"rotate_api_key":    "Rotate a service account API key.",
	"transcribe_audio":  "Transcribe an audio recording to text.",
	"resize_disk_image": "Resize a persistent disk image.",
}

// reactiveLLM decides from the declarations it is given, so each strategy is
// measured on the behaviour it actually induces.
type reactiveLLM struct {
	seen     [][]string
	calledFn map[string]bool
}

func (m *reactiveLLM) Name() string { return "reactive-llm" }

func (m *reactiveLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.calledFn == nil {
			m.calledFn = map[string]bool{}
		}
		var declared []string
		if req.Config != nil {
			for _, t := range req.Config.Tools {
				if t == nil {
					continue
				}
				for _, fd := range t.FunctionDeclarations {
					declared = append(declared, fd.Name)
				}
			}
		}
		slices.Sort(declared)
		m.seen = append(m.seen, declared)
		if len(m.seen) > 10 {
			yield(nil, fmt.Errorf("runaway: %d generations", len(m.seen)))
			return
		}

		call := func(name string) {
			m.calledFn[name] = true
			yield(&model.LLMResponse{
				Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{Name: name, Args: map[string]any{}}},
				}},
				TurnComplete: true,
			}, nil)
		}

		switch {
		case slices.Contains(declared, taskTool) && !m.calledFn[taskTool]:
			call(taskTool)
		case !slices.Contains(declared, taskTool) &&
			slices.Contains(declared, "search_tools") && !m.calledFn["search_tools"]:
			call("search_tools")
		default:
			yield(&model.LLMResponse{
				Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "done"}}},
				TurnComplete: true,
			}, nil)
		}
	}
}

type emptyArgs struct{}

type textResult struct {
	Text string `json:"text"`
}

// rank is the stand-in for BM25: which catalog entries answer this query.
func rank(query string) []string {
	q := strings.ToLower(query)
	var hits []string
	for name, desc := range catalog {
		for _, token := range strings.Fields(strings.ToLower(name + " " + desc)) {
			token = strings.Trim(token, ".,")
			if len(token) > 3 && strings.Contains(q, token) {
				hits = append(hits, name)
				break
			}
		}
	}
	slices.Sort(hits)
	return hits
}

func catalogTools(t *testing.T) map[string]tool.Tool {
	t.Helper()
	out := map[string]tool.Tool{}
	for name, desc := range catalog {
		ft, err := functiontool.New[emptyArgs, textResult](
			functiontool.Config{Name: name, Description: desc},
			func(ctx agent.Context, _ emptyArgs) (textResult, error) {
				return textResult{Text: "ok"}, nil
			})
		if err != nil {
			t.Fatalf("functiontool.New(%s) = %v", name, err)
		}
		out[name] = ft
	}
	return out
}

func searchTool(t *testing.T) tool.Tool {
	t.Helper()
	ft, err := functiontool.New[emptyArgs, textResult](
		functiontool.Config{Name: "search_tools", Description: "Find tools matching the task."},
		func(ctx agent.Context, _ emptyArgs) (textResult, error) {
			var names []any
			if v, err := ctx.State().Get(discoveredKey); err == nil {
				names, _ = v.([]any)
			}
			names = append(names, taskTool)
			if err := ctx.State().Set(discoveredKey, names); err != nil {
				return textResult{}, err
			}
			return textResult{Text: "revealed; continue on the next step"}, nil
		})
	if err != nil {
		t.Fatalf("functiontool.New(search_tools) = %v", err)
	}
	return ft
}

// A: everything declared up front.
type eagerToolset struct{ all map[string]tool.Tool }

func (ts *eagerToolset) Name() string { return "eager" }
func (ts *eagerToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	var out []tool.Tool
	for _, n := range slices.Sorted(maps(ts.all)) {
		out = append(out, ts.all[n])
	}
	return out, nil
}

// B: the design doc - bootstrap only, model must call search_tools.
type searchToolToolset struct {
	search tool.Tool
	all    map[string]tool.Tool
}

func (ts *searchToolToolset) Name() string { return "search_tool" }
func (ts *searchToolToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	out := []tool.Tool{ts.search}
	if v, err := ctx.ReadonlyState().Get(discoveredKey); err == nil {
		names, _ := v.([]any)
		for _, n := range names {
			if t, ok := ts.all[n.(string)]; ok {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// C: search from the user's message before the first generation, with
// search_tools kept as a fallback for anything the pre-search missed.
type preSearchToolset struct {
	search tool.Tool
	all    map[string]tool.Tool
	hits   []string // what the pre-search matched, for reporting
}

func (ts *preSearchToolset) Name() string { return "pre_search" }
func (ts *preSearchToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	out := []tool.Tool{ts.search}
	seen := map[string]bool{}
	if uc := ctx.UserContent(); uc != nil {
		var sb strings.Builder
		for _, p := range uc.Parts {
			sb.WriteString(p.Text)
		}
		ts.hits = rank(sb.String())
		for _, n := range ts.hits {
			seen[n] = true
			out = append(out, ts.all[n])
		}
	}
	if v, err := ctx.ReadonlyState().Get(discoveredKey); err == nil {
		names, _ := v.([]any)
		for _, n := range names {
			if s := n.(string); !seen[s] {
				seen[s] = true
				out = append(out, ts.all[s])
			}
		}
	}
	return out, nil
}

func maps(m map[string]tool.Tool) iter.Seq[string] {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func measure(t *testing.T, ts tool.Toolset, query string) (generations int, declaredFirst []string, taskRan bool) {
	t.Helper()
	m := &reactiveLLM{}
	a, err := llmagent.New(llmagent.Config{Name: "a", Model: m, Toolsets: []tool.Toolset{ts}})
	if err != nil {
		t.Fatalf("llmagent.New() = %v", err)
	}
	r, err := runner.New(runner.Config{
		AppName: "presearch", Agent: a,
		SessionService: session.InMemoryService(), AutoCreateSession: true,
	})
	if err != nil {
		t.Fatalf("runner.New() = %v", err)
	}
	msg := genai.NewContentFromText(query, genai.RoleUser)
	for ev, err := range r.Run(t.Context(), "u", "s", msg, agent.RunConfig{}) {
		if err != nil {
			t.Fatalf("run error: %v", err)
		}
		if ev == nil || ev.LLMResponse.Content == nil {
			continue
		}
		for _, p := range ev.LLMResponse.Content.Parts {
			if fr := p.FunctionResponse; fr != nil && fr.Name == taskTool {
				if _, isErr := fr.Response["error"]; !isErr {
					taskRan = true
				}
			}
		}
	}
	return len(m.seen), m.seen[0], taskRan
}

func TestGenerationsPerStrategy(t *testing.T) {
	all := catalogTools(t)

	for _, tc := range []struct {
		name  string
		ts    tool.Toolset
		query string
	}{
		{"A eager (all declared up front)", &eagerToolset{all: all}, userQuery},
		{"B searchTool (design doc)", &searchToolToolset{search: searchTool(t), all: all}, userQuery},
		{"C preSearch, query hits", &preSearchToolset{search: searchTool(t), all: all}, userQuery},
		{"D preSearch, query misses", &preSearchToolset{search: searchTool(t), all: all}, missQuery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gens, first, ran := measure(t, tc.ts, tc.query)
			t.Logf("generations=%d  taskToolRan=%v", gens, ran)
			t.Logf("generation 1 declared %d tools: %v", len(first), first)
			if !ran {
				t.Errorf("%s never ran %s", tc.name, taskTool)
			}
		})
	}
}
