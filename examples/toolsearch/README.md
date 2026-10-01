# ADK Tool Search

A small reference for **on-demand tool and skill discovery in ADK Go**.
An agent starts with a search tool and an optional core set, then loads other
tool definitions only when it needs them. This keeps large catalogs out of the
initial model request without hiding their capabilities entirely.

**Heavily inspired by [Anthropic's tool search implementation](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool).**
That implementation should continue to inform this design. This is a
client-side ADK implementation, not Anthropic's server-side tool.

## Run locally

Run from the repository root:

```sh
go run ./examples/toolsearch -offline
go run ./examples/toolsearch -offline -skill-first
```

The offline mode runs a **scripted model through the real ADK agent and runner**.
It checks that:

1. Initially, only search and skill-management tools are declared.
2. Tool-first mode discovers `multiply_numbers` with a `connected_skill` hint.
   Skill-first mode discovers `multiplication` without loading any arithmetic tool.
3. Loading `multiplication` activates its instructions and reveals its associated
   `multiply_numbers` tool on the next model step.
4. The agent loads the skill's example resource, then actually computes
   `6 * 7 = 42`. The unselected add tool and inactive skill body stay hidden.

It prints search/tool calls, tool responses, and a final verification message.
This is an integration smoke test, **not a test of a live model's search choices**.
Offline mode always uses this fixed scenario; `-prompt` does not change its script.
`-skill-first` affects only offline mode.

### Live Gemini agent

Set your own Gemini API key in your shell (do not commit it):

```sh
export GOOGLE_API_KEY='your-api-key'
go run ./examples/toolsearch -prompt "Use a tool to multiply 6 by 7."
go run ./examples/toolsearch -prompt "Use a tool to add 19 and 23."
# Override the model if needed:
go run ./examples/toolsearch -model gemini-flash-latest -prompt "Use a tool to multiply 12 by 9."
```

Live mode sends the prompt and tool metadata to Gemini and can incur API charges.
It requires model access for your account. The program uses the Gemini Developer
API, a two-minute timeout, and a fresh in-memory session per process.
No web server, database, cloud deployment, or application credentials are needed.

## Read the code

- [main.go](main.go): two arithmetic tools, a catalog, the gated toolset,
  one LLM agent, an in-memory session service, and the runner.
- [tool/toolsearch](../../tool/toolsearch/toolsearch.go): the reusable wrapper,
  search function, discovery state, and per-request declaration loading.
- [internal/ranksearch](../../internal/ranksearch): exact selection, regex, BM25,
  tokenization, ranking limits, and regression tests.
- [tool/skillsearch](../../tool/skillsearch/skillsearch.go): ranked skill discovery,
  session activation, connected-tool reveal, and active-instruction injection.
- [skills](skills): embedded, generic addition and multiplication
  skills, including an example resource.
- [offline.go](offline.go): the deterministic smoke-test model.
- [main_test.go](main_test.go): the end-to-end ADK runner check.

The demo catalog is deliberately tiny so the mechanism is easy to follow.
The intended benefit appears with larger tool catalogs; this example does not
claim measured token savings or latency improvements.

## How discovery works

**Catalog → search → session state → next model request → execution.**

1. Wrap a normal ADK `tool.Toolset` with `toolsearch.New`.
2. `Tools()` returns `search_tools`, core tools, and previously discovered tools.
   Gated tools' schemas are absent initially.
3. The model calls `search_tools`. The wrapper reads the current base catalog,
   searches eligible metadata, and returns names, short descriptions, and
   optional skill hints—not full schemas.
4. Matching names are appended to agent-namespaced session state.
5. ADK caches the initial tool list within an invocation. The wrapper's
   **`ProcessRequest` hook** therefore packs newly discovered tools on each
   subsequent model step, both their declarations and executable handles.
6. The model calls the discovered tool on the next step. No new user message
   is needed. The search and newly discovered tool must not be called in the
   same model response.

On later invocations, `Tools()` includes the persisted discoveries immediately.
The wrapper also forwards `ProcessRequest` to a base toolset that implements it.
Tools must support ADK's structural `ProcessRequest` hook to be searchable;
non-packable tools are omitted from search results.

## Search behavior

| Query | Behavior |
| --- | --- |
| `multiply product` | BM25 over names, descriptions, and supported top-level argument names/descriptions |
| `select:multiply_numbers,add_numbers` | Case-sensitive exact names, in requested order; unknown names reported |
| `^multiply_.*` | Case-insensitive Go regex over names and descriptions; name hits first, then alphabetical |

- **BM25:** `k1=1.5`, `b=0.75`; name tokens repeated three times.
  Tokenization splits punctuation and camelCase, lowercases, and lightly stems
  English plurals. No embeddings, LLM ranking call, or external search service.
- **Relevance floor:** BM25 results below 10% of the best score are discarded.
  Equal scores retain catalog order. Statistics use the remaining eligible catalog.
- **Limits:** default eight results for BM25/regex; configurable via `MaxResults`.
  Exact selection is not result-capped. All queries are limited to 200 Unicode
  characters; returned descriptions to 200 bytes without splitting UTF-8 runes.
- **Query dispatch:** any regex metacharacter selects regex mode, including
  punctuation such as a period or question mark. Invalid regex falls back to a
  literal case-insensitive match. A regex-mode query that matches nothing and
  contains whitespace is ranked by BM25 instead, with a note saying so, so
  "how do I list files?" still finds `list_files` while a pattern such as
  `^get_.*_record$` that matches nothing returns nothing. This is a heuristic,
  not semantic search.
- **Exclusions:** core and already-discovered tools are not returned again.
  If every base tool is core (or the catalog is empty), no search tool is exposed.
  Search remains present after all initially gated tools have been discovered.
- **Arguments:** indexing supports `*jsonschema.Schema` in
  `ParametersJsonSchema`, and typed `genai.Schema` parameters. Arbitrary JSON
  schema maps and nested argument properties are not indexed.
- **Errors:** no matches, excessive query length, unknown selections, and catalog
  unavailability produce notes. State-write and request-packing failures propagate
  as errors. Catalog errors during search currently return a generic note.

## Use the wrapper in another agent

```go
gated, err := toolsearch.New(baseToolset, toolsearch.Config{
    AgentName:     "assistant",
    CoreToolNames: []string{"get_current_time"},
    GatedToolNames: []string{"find_files", "read_file"},
    MaxResults:   8,
})
// Handle err, then set llmagent.Config.Toolsets to []tool.Toolset{gated}.
```

- **`AgentName`** must match your chosen state namespace and be unique among
  agents that should not share discoveries. It must not contain a colon.
- **`CoreToolNames`** stay visible without search.
- **`GatedToolNames`** is an optional sorted name-only advertisement in the search
  description. It does not define or restrict the actual catalog. Omit it when
  a full name list is too large or cannot be kept current.
- **`SkillAnnotations`** optionally maps tool names to `connected_skill` hints.
  These are advisory metadata, not authorization or prerequisites. The demo
  connects these hints to the included `load_skill` tool.
- **`RevealTools(ctx.State(), agentName, names...)`** programmatically reveals
  tools through the same mechanism, for example from an application-owned skill
  loader. Duplicate and empty names are ignored. Names absent from the catalog
  do not become executable.

Use unique tool names without commas; reserve `search_tools` for the wrapper.
Pass a non-nil base toolset and treat configuration slices/maps as immutable.
The name-only advertisement must contain only names the user may see.

## Skill search and connected skills

The connection is **bidirectional**, but neither direction grants permissions:

- **Tool → skill:** `search_tools` returns an optional `connected_skill` name.
  The tool is usable without loading that skill; its hint is advisory.
- **Skill → tool:** `load_skill` activates instructions and calls
  `toolsearch.RevealTools` for that skill's configured tool names. The tools
  still need to exist in the agent's authorized base catalog.

`search_skills` uses the same exact-name, regex, and BM25 ranker as tool search.
It indexes **names and descriptions, not instruction bodies**, with name weighting,
an eight-result BM25/regex cap, a 10% relevance floor, a 200-character query
limit, and 1,000-byte result descriptions. Exact selection is not result-capped.
Already-active skills across the session's agent namespaces are excluded.
Searching does not activate anything; the model chooses a result to load.
Metadata/token bags are built once when the toolset is constructed; ranking
statistics are still computed per query.

The skill toolset exposes:

| Tool | Effect |
| --- | --- |
| `search_skills` | Return ranked skill metadata and a loading suggestion |
| `load_skill` | Persist instructions and reveal associated tools |
| `deactivate_skill` | Stop injecting this agent's skill instructions |
| `load_skill_resource` | Read supporting files through the `skilltoolset` resource tool |

It replaces `skilltoolset`'s `list_skills` and full-catalog prompt injection. Only a
short usage instruction and **active skill bodies** are added to each request.
The search declaration advertises a sorted name-only catalog.

Skills are ordinary `SKILL.md` directories with YAML `name` and `description`
frontmatter. The demo uses `embed.FS` and
`skill.NewFileSystemSource`, so resource access is confined to the embedded
examples, not your working directory. Resources under `references/`, `assets/`,
and `scripts/` can be read; scripts are not executed.

```go
skills, err := skillsearch.New(ctx, source, skillsearch.Config{
    AgentName: "assistant", // Same namespace as toolsearch.Config.AgentName.
    ToolNames: map[string][]string{
        "file-reading": {"find_files", "read_file"},
    },
})
// Handle err, then register both toolsets:
// Toolsets: []tool.Toolset{gated, skills}
// Also set toolsearch.Config.SkillAnnotations for tool-to-skill hints.
```

Instruction state uses `skills:active:<agent>:<skill>`. Other agents in the
**same trusted session** inherit active instructions; an agent's own version
takes precedence. Repeated loading is idempotent and re-attempts connected-tool
reveal. An inherited activation cannot be deactivated by another agent.
Deactivation does **not** remove revealed tools or old conversation messages.

Optional `metadata.require-confirmation: "true"` uses ADK's confirmation API
before activation. The included skills need no approval. The minimal CLI has no
interactive confirmation/resume UI; embedding applications must supply that.

This reference implements local skill discovery and activation, not a general
workflow backend. Remote workflow search, reference expansion, feature-flag
filtering, conditional instruction sections, and automatic skill dependencies
are not implemented. Filter the source before registration for access control;
do not rely on unsupported metadata to enforce policy. Rebuild the toolset after
catalog changes. Only register trusted skill instructions and resources.

## State, safety, and scope

Each discovered tool is stored under its own key,
`tool_search:discovered:<AgentName>:<tool>`, with its discovery position as the
value. Their lifetime is the session's lifetime,
not the process's search index. A persistent ADK session service can retain them
across invocations; this demo intentionally uses in-memory storage.
Discoveries append in stable order and are not automatically evicted.

**Discovery is not authorization.** The base catalog must already be filtered
for the caller, and tool execution must enforce permissions. A discovered name
never grants access by itself.

The base catalog is read again during search and packing, but this is **not a
complete dynamic-catalog/revocation implementation**: ADK may retain initially
packed tools for the invocation, and an initially all-core catalog has no search
tool to activate mid-invocation. Restart the invocation when authorization or
catalog membership changes. Static advertisements can become stale.

One key per tool means discoveries from several function calls in one model
response are all kept, even though each call writes its own state delta. Calls
that saw the same prior state can store equal positions, and ties are ordered by
name. There is no per-session discovery budget, TTL, or reset tool here.

## Caching is important—and still underexplored

**Caching deserves further work and has not been substantially explored in this
implementation.**

- **Search computation:** tool token bags and BM25 corpus statistics are rebuilt on
  each tool search. Skill token bags are built once per toolset; their BM25
  statistics are rebuilt per query. There is no persistent index, shared indexing service, result
  cache, or embedding cache. Profile realistic catalogs before adding a cache.
  Any cache needs catalog identity/version and authorization scope in its key.
- **Provider prompt caching:** sorted advertisements and discovery-order appends
  aim to avoid unnecessary declaration reordering. They do not guarantee cache
  hits; provider behavior and the rest of the prompt matter. No explicit provider
  cache integration or measured hit-rate improvement is included.
- **Trade-off:** discoveries accumulate, so long sessions can eventually expose
  much of the catalog. Eviction may save tokens but also disrupt cache reuse.

Useful next measurements are initial/steady-state input tokens, search latency,
discovery quality, cache hit rates, and the cost of reloading evicted tools.

## Scope

**Included:** the toolset wrapper, all three query modes, ranking/tokenization,
argument metadata search, result limits/notes, per-agent discovery state,
programmatic reveal, bidirectional tool/skill connections, ranked skill search,
skill activation/deactivation, active-instruction injection, resource loading,
confirmation hooks, deterministic loading order, base request-hook forwarding,
and regression tests.

**Outside scope:** external business APIs, authentication, remote workflow
services, telemetry, and deployment infrastructure. The demo uses two arithmetic
tools and generic local skills with no external services in offline mode.

This is a reference implementation, not a production framework or a compatibility
promise. There is no claim of Anthropic API compatibility, semantic retrieval,
or production-scale benchmarking.

## Verify

```sh
go test -race ./tool/toolsearch/... ./tool/skillsearch/... ./internal/ranksearch/... ./examples/toolsearch/...
go run ./examples/toolsearch -offline
go run ./examples/toolsearch -offline -skill-first
```

The tests cover ranking, exact/regex selection, limits, discovery order,
core exclusions, optional annotations, programmatic reveal, argument-only search,
and request packing. Skill tests cover search modes, activation, deactivation,
inheritance, approval, invalid requests, and resource-path restrictions.
The runner test checks actual model-visible schemas, connected-skill hints,
active instructions, resource loading, and execution in both discovery directions.
These checks need no model credentials; test live model behavior separately.
