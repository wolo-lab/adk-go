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

package main

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

// scriptedModel checks discovery through the real runner without an external LLM.
// It deliberately handles only the fixed 6 * 7 smoke-test scenario.
type scriptedModel struct {
	step       int
	skillFirst bool
}

func (*scriptedModel) Name() string { return "offline-script" }

func (s *scriptedModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		fail := func(message string) { yield(nil, fmt.Errorf("offline check: %s", message)) }
		declared := make(map[string]bool)
		if req.Config != nil {
			for _, t := range req.Config.Tools {
				for _, d := range t.FunctionDeclarations {
					declared[d.Name] = true
				}
			}
		}
		if !declared["search_tools"] {
			fail("search_tools must be declared")
			return
		}
		if declared["add_numbers"] {
			fail("unselected tool leaked into declarations")
			return
		}
		var instruction string
		if req.Config.SystemInstruction != nil {
			for _, p := range req.Config.SystemInstruction.Parts {
				instruction += p.Text
			}
		}
		active := strings.Contains(instruction, "<skill name=\"multiplication\">")
		if active != (s.step >= 2) {
			fail("skill instructions injected at wrong step")
			return
		}
		if strings.Contains(instruction, "<skill name=\"addition\">") {
			fail("inactive skill body leaked")
			return
		}
		var part *genai.Part
		switch s.step {
		case 0:
			if declared["multiply_numbers"] {
				fail("gated tool visible before search")
				return
			}
			name := "search_tools"
			if s.skillFirst {
				name = "search_skills"
			}
			part = &genai.Part{FunctionCall: &genai.FunctionCall{Name: name, Args: map[string]any{"query": "multiply product"}}}
		case 1:
			if declared["multiply_numbers"] == s.skillFirst {
				fail("search activation semantics incorrect")
				return
			}
			if !s.skillFirst {
				found := false
				for _, c := range req.Contents {
					for _, p := range c.Parts {
						if p.FunctionResponse != nil && p.FunctionResponse.Name == "search_tools" &&
							strings.Contains(fmt.Sprint(p.FunctionResponse.Response), "connected_skill:multiplication") {
							found = true
						}
					}
				}
				if !found {
					fail("missing connected_skill hint")
					return
				}
			}
			part = &genai.Part{FunctionCall: &genai.FunctionCall{Name: "load_skill", Args: map[string]any{"name": "multiplication"}}}
		case 2:
			if !declared["multiply_numbers"] {
				fail("skill did not reveal connected tool")
				return
			}
			part = &genai.Part{FunctionCall: &genai.FunctionCall{Name: "load_skill_resource", Args: map[string]any{"skill_name": "multiplication", "resource_path": "references/examples.md"}}}
		case 3:
			found := false
			for _, c := range req.Contents {
				for _, p := range c.Parts {
					if p.FunctionResponse != nil && p.FunctionResponse.Name == "load_skill_resource" &&
						strings.Contains(fmt.Sprint(p.FunctionResponse.Response), "Product formatting") {
						found = true
					}
				}
			}
			if !found {
				fail("skill resource was not loaded")
				return
			}
			if !declared["multiply_numbers"] || req.Tools["multiply_numbers"] == nil {
				fail("discovered tool not packed on next step")
				return
			}
			part = &genai.Part{FunctionCall: &genai.FunctionCall{Name: "multiply_numbers", Args: map[string]any{"a": 6.0, "b": 7.0}}}
		case 4:
			found := false
			for _, c := range req.Contents {
				for _, p := range c.Parts {
					if p.FunctionResponse != nil && p.FunctionResponse.Name == "multiply_numbers" && fmt.Sprint(p.FunctionResponse.Response["value"]) == "42" {
						found = true
					}
				}
			}
			if !found {
				fail("expected actual tool result 42")
				return
			}
			part = &genai.Part{Text: "6 × 7 = 42 (offline discovery and execution verified)."}
		default:
			fail("unexpected extra model step")
			return
		}
		s.step++
		yield(&model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{part}}}, nil)
	}
}
