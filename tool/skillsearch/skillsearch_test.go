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

package skillsearch

import (
	"fmt"
	"iter"
	"strings"
	"testing"
	"testing/fstest"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/internal/ranksearch"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"
	"google.golang.org/adk/v2/tool/toolconfirmation"
)

type fakeState map[string]any

func (s fakeState) Get(k string) (any, error) {
	v, ok := s[k]
	if !ok {
		return nil, fmt.Errorf("missing key")
	}
	return v, nil
}
func (s fakeState) Set(k string, v any) error { s[k] = v; return nil }
func (s fakeState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s {
			if !yield(k, v) {
				return
			}
		}
	}
}

type fakeContext struct {
	agent.StrictContextMock
	state        fakeState
	confirmation *toolconfirmation.ToolConfirmation
	requested    bool
}

func (c *fakeContext) State() session.State                                 { return c.state }
func (c *fakeContext) ReadonlyState() session.ReadonlyState                 { return c.state }
func (c *fakeContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return c.confirmation }
func (c *fakeContext) RequestConfirmation(string, any) error                { c.requested = true; return nil }

func setup(t *testing.T) (*Toolset, *fakeContext) {
	t.Helper()
	source := skill.NewFileSystemSource(fstest.MapFS{
		"multiply/SKILL.md":  &fstest.MapFile{Data: []byte("---\nname: multiply\ndescription: Multiply numbers and calculate products.\n---\nUse the multiplication tool.")},
		"add/SKILL.md":       &fstest.MapFile{Data: []byte("---\nname: add\ndescription: Add numbers and calculate sums.\n---\nUse the addition tool.")},
		"confirmed/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: confirmed\ndescription: A confirmation example.\nmetadata:\n  require-confirmation: \"true\"\n---\nReport confirmation.")},
	})
	ts, err := New(t.Context(), source, Config{AgentName: "test", ToolNames: map[string][]string{"multiply": {"multiply_numbers"}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return ts, &fakeContext{StrictContextMock: agent.NewStrictContextMock(t.Context()), state: fakeState{}}
}

// systemInstruction runs ProcessRequest and returns the injected instruction.
func systemInstruction(t *testing.T, ts *Toolset, ctx *fakeContext) string {
	t.Helper()
	req := &model.LLMRequest{}
	if err := ts.ProcessRequest(ctx, req); err != nil {
		t.Fatalf("ProcessRequest() error = %v", err)
	}
	return req.Config.SystemInstruction.Parts[0].Text
}

func mustLoad(t *testing.T, ts *Toolset, ctx *fakeContext, name string) map[string]string {
	t.Helper()
	out, err := ts.load(ctx, name)
	if err != nil {
		t.Fatalf("load(%q) error = %v", name, err)
	}
	return out
}

func checkDiscovered(t *testing.T, ctx *fakeContext, want string) {
	t.Helper()
	if got := ctx.state["tool_search:discovered:test"]; got != want {
		t.Errorf("discovered tools = %v, want %q", got, want)
	}
}

func TestSearchLoadAndDeactivate(t *testing.T) {
	ts, ctx := setup(t)
	for _, query := range []string{"multiply products", "^multiply$", "select:multiply"} {
		out := ts.search(ctx.state, query)
		if len(out.Matches) != 1 || out.Matches[0].Name != "multiply" {
			t.Errorf("search(%q) = %v, want only multiply", query, out.Matches)
		}
	}
	if len(ctx.state) != 0 {
		t.Errorf("state = %v, want empty: search must not activate skills or tools", ctx.state)
	}
	if got := systemInstruction(t, ts, ctx); strings.Contains(got, "Use the multiplication tool.") {
		t.Error("inactive skill body was injected")
	}

	if out := mustLoad(t, ts, ctx, "multiply"); out["status"] != "active" {
		t.Errorf("load() status = %q, want active", out["status"])
	}
	checkDiscovered(t, ctx, "multiply_numbers")
	if got := ts.search(ctx.state, "select:multiply").Matches; len(got) != 0 {
		t.Errorf("search() after load = %v, want active skill excluded", got)
	}
	got := systemInstruction(t, ts, ctx)
	if !strings.Contains(got, "Use the multiplication tool.") {
		t.Error("active skill body was not injected")
	}
	if strings.Contains(got, "Use the addition tool.") {
		t.Error("inactive skill body was injected")
	}

	mustLoad(t, ts, ctx, "multiply")
	checkDiscovered(t, ctx, "multiply_numbers") // reloading must not duplicate tools
	if _, err := ts.deactivate(ctx.state, "multiply"); err != nil {
		t.Fatalf("deactivate() error = %v", err)
	}
	if got := activeInstructions(ctx.state, "test"); len(got) != 0 {
		t.Errorf("activeInstructions() = %v, want none", got)
	}
	if got := ts.search(ctx.state, "select:multiply").Matches; len(got) != 1 {
		t.Errorf("search() after deactivate = %v, want 1 match", got)
	}
	checkDiscovered(t, ctx, "multiply_numbers") // deactivation is not tool revocation
}

func TestInheritedSkillsAndOwnPrecedence(t *testing.T) {
	ts, ctx := setup(t)
	ctx.state["skills:active:other:multiply"] = "Inherited instructions"
	if got := ts.search(ctx.state, "select:multiply").Matches; len(got) != 0 {
		t.Errorf("search() = %v, want inherited skill excluded", got)
	}
	out, err := ts.deactivate(ctx.state, "multiply")
	if err != nil {
		t.Fatalf("deactivate() error = %v", err)
	}
	if out["status"] != "INHERITED" {
		t.Errorf("deactivate() status = %q, want INHERITED", out["status"])
	}
	mustLoad(t, ts, ctx, "multiply")
	checkDiscovered(t, ctx, "multiply_numbers")
	if _, ok := ctx.state[ts.key("multiply")]; ok {
		t.Error("inherited activation was copied into this agent's namespace")
	}
	ctx.state[ts.key("multiply")] = "Own instructions"
	if got := activeInstructions(ctx.state, "test")["multiply"]; got != "Own instructions" {
		t.Errorf("activeInstructions()[multiply] = %q, want own instructions", got)
	}
}

func TestConfirmationBeforeActivation(t *testing.T) {
	ts, ctx := setup(t)
	out := mustLoad(t, ts, ctx, "confirmed")
	if !ctx.requested {
		t.Error("confirmation was not requested")
	}
	if out["status"] != "AWAITING_USER_INPUT" || len(ctx.state) != 0 {
		t.Errorf("load() status = %q, state = %v, want AWAITING_USER_INPUT and empty state", out["status"], ctx.state)
	}
	ctx.confirmation = &toolconfirmation.ToolConfirmation{Confirmed: false}
	out = mustLoad(t, ts, ctx, "confirmed")
	if out["status"] != "CANCELED" || len(ctx.state) != 0 {
		t.Errorf("load() status = %q, state = %v, want CANCELED and empty state", out["status"], ctx.state)
	}
	ctx.confirmation.Confirmed = true
	if out := mustLoad(t, ts, ctx, "confirmed"); out["status"] != "active" {
		t.Errorf("load() status = %q, want active", out["status"])
	}
}

func TestInvalidRequestsAndResourceBoundary(t *testing.T) {
	ts, ctx := setup(t)
	if _, err := ts.load(ctx, "../unknown"); err == nil {
		t.Error("load(../unknown) error = nil, want error")
	}
	if _, err := ts.deactivate(ctx.state, "other:multiply"); err == nil {
		t.Error("deactivate(other:multiply) error = nil, want error")
	}
	if got := ts.search(ctx.state, strings.Repeat("a", 201)).Note; !strings.Contains(got, "too long") {
		t.Errorf("search() note = %q, want it to contain %q", got, "too long")
	}
	if got := ts.search(ctx.state, "select:unknown").Note; !strings.Contains(got, "not found") {
		t.Errorf("search() note = %q, want it to contain %q", got, "not found")
	}
	if _, err := ts.source.LoadResource(t.Context(), "multiply", "../add/SKILL.md"); err == nil {
		t.Error("LoadResource(../add/SKILL.md) error = nil, want error")
	}
	if len(ctx.state) != 0 {
		t.Errorf("state = %v, want empty", ctx.state)
	}
}

func TestSearchLimitsAndActiveExclusion(t *testing.T) {
	ts, ctx := setup(t)
	ts.items = nil
	var names []string
	for i := range 10 {
		name := fmt.Sprintf("example-%d", i)
		desc := strings.Repeat("example ", 160)
		names = append(names, name)
		ts.items = append(ts.items, ranksearch.Item{Name: name, Description: desc, Tokens: ranksearch.BuildTokens(name, desc)})
	}
	ctx.state["skills:active:other:example-0"] = "Already active"
	out := ts.search(ctx.state, "example")
	if len(out.Matches) != 8 {
		t.Errorf("search() returned %d matches, want 8", len(out.Matches))
	}
	if !strings.Contains(out.Note, "showing 8 of 9") {
		t.Errorf("search() note = %q, want it to contain %q", out.Note, "showing 8 of 9")
	}
	for _, match := range out.Matches {
		if match.Name == "example-0" {
			t.Error("active skill example-0 was returned")
		}
		if len(match.Description) > 1000 {
			t.Errorf("len(description) = %d, want <= 1000", len(match.Description))
		}
	}
	if got := ts.search(ctx.state, "select:"+strings.Join(names, ",")).Matches; len(got) != 9 {
		t.Errorf("select search returned %d matches, want 9", len(got))
	}
}
