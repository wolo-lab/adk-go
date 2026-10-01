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
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/skillsearch"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"
	"google.golang.org/adk/v2/tool/toolsearch"
)

//go:embed skills
var exampleSkills embed.FS

func main() {
	offline := flag.Bool("offline", false, "Run a scripted model through the real ADK runner; no credentials or model API calls")
	skillFirst := flag.Bool("skill-first", false, "Start the offline scenario with search_skills instead of search_tools")
	modelName := flag.String("model", "gemini-flash-latest", "Gemini model name")
	prompt := flag.String("prompt", "Use a tool to multiply 6 by 7.", "Task for the agent")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var llm model.LLM
	if *offline {
		llm = &scriptedModel{skillFirst: *skillFirst}
	} else {
		if os.Getenv("GOOGLE_API_KEY") == "" {
			log.Fatal("Set GOOGLE_API_KEY, or use -offline")
		}
		var err error
		llm, err = gemini.NewModel(ctx, *modelName, &genai.ClientConfig{APIKey: os.Getenv("GOOGLE_API_KEY"), Backend: genai.BackendGeminiAPI})
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := run(ctx, llm, *prompt, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

type numbers struct {
	A float64 `json:"a" jsonschema:"First number."`
	B float64 `json:"b" jsonschema:"Second number."`
}

type result struct {
	Value float64 `json:"value"`
}

type catalog []tool.Tool

func (catalog) Name() string                                       { return "arithmetic" }
func (c catalog) Tools(agent.ReadonlyContext) ([]tool.Tool, error) { return c, nil }

func run(ctx context.Context, llm model.LLM, prompt string, out io.Writer) error {
	add, err := functiontool.New(functiontool.Config{Name: "add_numbers", Description: "Add two numbers."},
		func(_ agent.Context, args numbers) (result, error) { return result{args.A + args.B}, nil })
	if err != nil {
		return err
	}
	multiply, err := functiontool.New(functiontool.Config{Name: "multiply_numbers", Description: "Multiply two numbers to calculate their product."},
		func(_ agent.Context, args numbers) (result, error) { return result{args.A * args.B}, nil })
	if err != nil {
		return err
	}
	gated, err := toolsearch.New(catalog{add, multiply}, toolsearch.Config{
		AgentName:        "calculator",
		GatedToolNames:   []string{"add_numbers", "multiply_numbers"},
		SkillAnnotations: map[string]string{"add_numbers": "addition", "multiply_numbers": "multiplication"},
	})
	if err != nil {
		return err
	}
	skillFS, err := fs.Sub(exampleSkills, "skills")
	if err != nil {
		return err
	}
	skills, err := skillsearch.New(ctx, skill.NewFileSystemSource(skillFS), skillsearch.Config{
		AgentName: "calculator",
		ToolNames: map[string][]string{"addition": {"add_numbers"}, "multiplication": {"multiply_numbers"}},
	})
	if err != nil {
		return err
	}
	a, err := llmagent.New(llmagent.Config{
		Name: "calculator", Model: llm,
		Instruction: "Help with arithmetic. Use tools for calculations and report the result concisely.",
		Toolsets:    []tool.Toolset{gated, skills},
	})
	if err != nil {
		return err
	}
	r, err := runner.New(runner.Config{
		AppName: "tool_search_demo", Agent: a,
		SessionService: session.InMemoryService(), AutoCreateSession: true,
	})
	if err != nil {
		return err
	}
	for event, err := range r.Run(ctx, "demo_user", "demo_session", genai.NewContentFromText(prompt, genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			return err
		}
		if event.Content == nil {
			continue
		}
		for _, part := range event.Content.Parts {
			var err error
			switch {
			case part.FunctionCall != nil:
				_, err = fmt.Fprintf(out, "call %s %v\n", part.FunctionCall.Name, part.FunctionCall.Args)
			case part.FunctionResponse != nil:
				_, err = fmt.Fprintf(out, "result %s %v\n", part.FunctionResponse.Name, part.FunctionResponse.Response)
			case part.Text != "":
				_, err = fmt.Fprintln(out, part.Text)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}
