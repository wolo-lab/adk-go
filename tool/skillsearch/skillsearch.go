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

// Package skillsearch adds ranked discovery and session activation to ADK skills.
package skillsearch

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/internal/ranksearch"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/skilltoolset"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"
	"google.golang.org/adk/v2/tool/toolsearch"
)

const activePrefix = "skills:active:"

// Config connects skills to tools in the same agent's searchable catalog.
type Config struct {
	AgentName string
	// ToolNames maps each skill name to tools revealed when it is loaded.
	// The tools must also exist in the base catalog wrapped by toolsearch.
	ToolNames map[string][]string
}

// Toolset exposes skill search, activation, deactivation, and resource loading.
type Toolset struct {
	source       skill.Source
	cfg          Config
	frontmatters map[string]*skill.Frontmatter
	items        []ranksearch.Item
	tools        []tool.Tool
}

// New snapshots skill metadata for ranked discovery without preloading bodies.
// Sources and skill contents must be trusted and filtered for the caller.
func New(ctx context.Context, source skill.Source, cfg Config) (*Toolset, error) {
	if source == nil || !validName(cfg.AgentName) {
		return nil, fmt.Errorf("source and a valid agent name are required")
	}
	fms, err := source.ListFrontmatters(ctx)
	if err != nil {
		return nil, err
	}
	t := &Toolset{source: source, cfg: cfg, frontmatters: make(map[string]*skill.Frontmatter)}
	t.cfg.ToolNames = make(map[string][]string, len(cfg.ToolNames))
	for name, tools := range cfg.ToolNames {
		t.cfg.ToolNames[name] = slices.Clone(tools)
	}
	slices.SortFunc(fms, func(a, b *skill.Frontmatter) int { return strings.Compare(a.Name, b.Name) })
	var names []string
	for _, fm := range fms {
		if !validName(fm.Name) || t.frontmatters[fm.Name] != nil {
			return nil, fmt.Errorf("invalid or duplicate skill name %q", fm.Name)
		}
		t.frontmatters[fm.Name] = fm
		names = append(names, fm.Name)
		t.items = append(t.items, ranksearch.Item{Name: fm.Name, Description: fm.Description, Tokens: ranksearch.BuildTokens(fm.Name, fm.Description)})
	}
	for name := range cfg.ToolNames {
		if t.frontmatters[name] == nil {
			return nil, fmt.Errorf("tool binding names unknown skill %q", name)
		}
	}
	search, err := functiontool.New(functiontool.Config{
		Name:        "search_skills",
		Description: ranksearch.DescribeSearch("Find relevant skills by natural-language task, regex, or select:name1,name2. Search reads metadata only; call load_skill with a result name to activate its instructions.", names),
	},
		func(ctx agent.Context, args queryArgs) (searchOutput, error) {
			return t.search(ctx.ReadonlyState(), args.Query), nil
		})
	if err != nil {
		return nil, err
	}
	load, err := functiontool.New(functiontool.Config{
		Name:        "load_skill",
		Description: "Activate a skill by exact name. Its instructions and associated tools become available on the next model step. Finish this response before using newly revealed tools. Skills marked require-confirmation need user approval.",
	},
		func(ctx agent.Context, args nameArgs) (map[string]string, error) { return t.load(ctx, args.Name) })
	if err != nil {
		return nil, err
	}
	deactivate, err := functiontool.New(functiontool.Config{
		Name:        "deactivate_skill",
		Description: "Stop including this agent's active skill instructions in future requests. This does not revoke tools or erase conversation history. Skills active in another agent's namespace cannot be deactivated here.",
	},
		func(ctx agent.Context, args nameArgs) (map[string]string, error) {
			return t.deactivate(ctx.State(), args.Name)
		})
	if err != nil {
		return nil, err
	}
	inner, err := skilltoolset.New(ctx, skilltoolset.Config{Source: source})
	if err != nil {
		return nil, err
	}
	innerTools, err := inner.Tools(nil)
	if err != nil {
		return nil, err
	}
	t.tools = []tool.Tool{search, load, deactivate}
	for _, tool := range innerTools {
		if tool.Name() == "load_skill_resource" {
			t.tools = append(t.tools, tool)
		}
	}
	return t, nil
}

type queryArgs struct {
	Query string `json:"query" jsonschema:"Task, regex, or select:name1,name2. Maximum 200 characters."`
}
type nameArgs struct {
	Name string `json:"name" jsonschema:"Exact skill name."`
}
type searchOutput struct {
	Matches  []ranksearch.Match `json:"matches"`
	Note     string             `json:"note,omitempty"`
	NextStep string             `json:"next_step,omitempty"`
}

func (t *Toolset) search(state session.ReadonlyState, query string) searchOutput {
	active := make(map[string]bool)
	for name := range activeInstructions(state, t.cfg.AgentName) {
		active[name] = true
	}
	matches, note := ranksearch.Rank(t.items, query, active, ranksearch.Config{
		ItemNoun: "skill", MaxResults: 8, MinScoreRatio: 0.1, MaxDescLen: 1000,
	})
	out := searchOutput{Matches: matches, Note: note}
	if len(matches) > 0 {
		out.NextStep = "Call load_skill with the most relevant match's name."
	}
	return out
}

func (t *Toolset) load(ctx agent.Context, name string) (map[string]string, error) {
	fm := t.frontmatters[name]
	if fm == nil {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	state := ctx.State()
	if activeInstructions(state, t.cfg.AgentName)[name] == "" {
		if fm.Metadata["require-confirmation"] == "true" {
			confirmation := ctx.ToolConfirmation()
			if confirmation == nil {
				if err := ctx.RequestConfirmation(name, map[string]string{"name": name, "description": fm.Description}); err != nil {
					return nil, err
				}
				return map[string]string{"status": "AWAITING_USER_INPUT"}, nil
			}
			if !confirmation.Confirmed {
				return map[string]string{"status": "CANCELED"}, nil
			}
		}
		body, err := t.source.LoadInstructions(ctx, name)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("skill %q has no instructions", name)
		}
		if err := state.Set(t.key(name), body); err != nil {
			return nil, err
		}
	}
	// A retry also reveals tools if a previous state write succeeded but reveal failed.
	if err := toolsearch.RevealTools(state, t.cfg.AgentName, t.cfg.ToolNames[name]...); err != nil {
		return nil, fmt.Errorf("skill active but tool reveal failed: %w", err)
	}
	return map[string]string{"status": "active", "next_steps": "Follow the skill instructions in your next model response."}, nil
}

func (t *Toolset) deactivate(state session.State, name string) (map[string]string, error) {
	if !validName(name) {
		return nil, fmt.Errorf("invalid skill name")
	}
	value, _ := state.Get(t.key(name))
	if body, ok := value.(string); ok && body != "" {
		if err := state.Set(t.key(name), nil); err != nil {
			return nil, err
		}
		return map[string]string{"status": "OK. Deactivated."}, nil
	}
	if activeInstructions(state, t.cfg.AgentName)[name] != "" {
		return map[string]string{"status": "INHERITED"}, nil
	}
	return map[string]string{"status": "NOT_FOUND"}, nil
}

func (t *Toolset) key(name string) string { return activePrefix + t.cfg.AgentName + ":" + name }

func validName(name string) bool {
	return strings.TrimSpace(name) != "" && !strings.ContainsAny(name, ":<>\"&,")
}

// activeInstructions inherits session-wide activations, preferring this agent's
// version when names overlap. Sorted keys make cross-agent precedence stable.
func activeInstructions(state session.ReadonlyState, ownAgent string) map[string]string {
	entries := make(map[string]string)
	for key, value := range state.All() {
		rest, ok := strings.CutPrefix(key, activePrefix)
		if !ok {
			continue
		}
		agentName, name, ok := strings.Cut(rest, ":")
		body, isString := value.(string)
		if ok && validName(agentName) && validName(name) && isString && body != "" {
			entries[rest] = body
		}
	}
	out := make(map[string]string)
	keys := slices.Sorted(maps.Keys(entries))
	for _, key := range keys {
		agentName, name, _ := strings.Cut(key, ":")
		if out[name] == "" || agentName == ownAgent {
			out[name] = entries[key]
		}
	}
	return out
}

// Name implements tool.Toolset.
func (*Toolset) Name() string { return "SearchableSkills" }

// Tools exposes management tools, not the full skill catalog.
func (t *Toolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return slices.Clone(t.tools), nil
}

// ProcessRequest injects usage guidance and only active instruction bodies.
// The upstream hook is not forwarded because it advertises all skill metadata.
func (t *Toolset) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	instructions := "Use search_skills to discover relevant skills and load_skill to activate them. " +
		"Follow relevant active instructions without reloading. Use load_skill_resource for supporting files."
	active := activeInstructions(ctx.ReadonlyState(), t.cfg.AgentName)
	for _, name := range slices.Sorted(maps.Keys(active)) {
		instructions += "\n\n<skill name=\"" + name + "\">\n" + active[name] + "\n</skill>"
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	if req.Config.SystemInstruction == nil {
		req.Config.SystemInstruction = genai.NewContentFromText(instructions, genai.RoleUser)
	} else {
		req.Config.SystemInstruction.Parts = append(req.Config.SystemInstruction.Parts, genai.NewPartFromText(instructions))
	}
	return nil
}
