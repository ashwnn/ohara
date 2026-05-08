// Package mcp implements the Model Context Protocol server for Ohara.
//
// This exposes memory tools via MCP stdio transport so ANY agent
// (OpenCode, Claude Code, Cursor, Windsurf, etc.) can use Ohara's
// persistent memory just by adding it as an MCP server.
//
// Tool profiles allow agents to load only the tools they need:
//
//	ohara mcp                    → all 30 tools (default)
//	ohara mcp --tools=agent      → 25 tools agents actually use (per skill files)
//	ohara mcp --tools=admin      → 5 tools for TUI/CLI (delete, stats, timeline, merge, list_domains)
//	ohara mcp --tools=agent,admin → combine profiles
//	ohara mcp --tools=mem_save,mem_search → individual tool names
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	projectpkg "github.com/ashwnn/ohara/internal/project"
	"github.com/ashwnn/ohara/internal/store"
	"github.com/ashwnn/ohara/internal/token"
	"github.com/ashwnn/ohara/internal/util"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPConfig holds configuration for the MCP server.
type MCPConfig struct {
	DefaultProject string // Auto-detected project name, used when LLM sends empty project
}

var suggestTopicKey = store.SuggestTopicKey

var loadMCPStats = func(s *store.Store) (*store.Stats, error) {
	return s.Stats()
}

var loadMCPStatsCombined = func(s *store.Store) (*store.Stats, *store.PackStats, error) {
	stats, err := s.Stats()
	if err != nil {
		return nil, nil, err
	}
	memStats, err := s.MemoryStats()
	if err != nil {
		return stats, nil, nil // MemoryStats failure is non-fatal
	}
	return stats, memStats, nil
}

//
// "agent" — tools AI agents use during coding sessions:
//   mem_save, mem_search, mem_context, mem_session_summary,
//   mem_session_start, mem_session_end, mem_suggest_topic_key,
//   mem_capture_passive, mem_save_prompt, mem_pack, mem_prime,
//   mem_mark_used, mem_append_outcome, mem_resolve_conflict
//
// "admin" — tools for manual curation, TUI, and dashboards:
//   mem_update, mem_delete, mem_stats, mem_timeline, mem_merge_projects,
//   mem_list_domains
//
// "all" (default) — every tool registered.

// ProfileAgent contains the tool names that AI agents need.
// Sourced from actual skill files and memory protocol instructions
// across all 4 supported agents (Claude Code, OpenCode, Gemini CLI, Codex).
var ProfileAgent = map[string]bool{
	"mem_save":                   true, // proactive save — referenced 17 times across protocols
	"mem_search":                 true, // search past memories — referenced 6 times
	"mem_context":                true, // recent context from previous sessions — referenced 10 times
	"mem_session_summary":        true, // end-of-session summary — referenced 16 times
	"mem_session_start":          true, // register session start
	"mem_session_end":            true, // mark session completed
	"mem_suggest_topic_key":      true, // stable topic key for upserts — referenced 3 times
	"mem_capture_passive":        true, // extract learnings from text — referenced in Gemini/Codex protocol
	"mem_save_prompt":            true, // save user prompts
	"mem_update":                 true, // update observation by ID — skills say "use mem_update when you have an exact ID to correct"
	"mem_pack":                   true, // explicit context pack via memory_items — uses new memory foundation
	"mem_prime":                  true, // prime context pack with Knowledge vs Episode tier separation
	"mem_mark_used":              true, // record memory item usage — increments access_count
	"mem_append_outcome":         true, // append outcome record to memory item
	"mem_resolve_conflict":       true, // resolve detected memory conflicts
	"mem_forget":                 true, // archive a memory with a documented reason
	"mem_link":                   true, // create typed relation between memories
	"mem_unlink":                 true, // remove a relation
	"mem_related":                true, // traverse relations from a memory
	"mem_consolidate_candidates": true, // review consolidation candidates grouped by domain/kind
	"mem_mark_consolidated":      true, // archive candidate + source episodic memories after semantic save
	"mem_search_rerank":          true, // explicit slow-path LLM reranking
	"mem_feedback":               true, // apply explicit utility feedback for RL-style weighting
	"mem_graph_context":          true, // entity-centric graph traversal context
	"mem_extract_entities":       true, // heuristic entity extraction and linking
}

// ProfileAdmin contains tools for TUI, dashboards, and manual curation
// that are NOT referenced in any agent skill or memory protocol.
var ProfileAdmin = map[string]bool{
	"mem_delete":         true, // admin/tooling use — not referenced in any agent skill file
	"mem_stats":          true, // admin/tooling use — not referenced in any agent skill file
	"mem_timeline":       true, // admin/tooling use — not referenced in any agent skill file
	"mem_merge_projects": true, // destructive curation tool — not for agent use
	"mem_list_domains":   true, // list domains for a project
}

// Profiles maps profile names to their tool sets.
var Profiles = map[string]map[string]bool{
	"agent": ProfileAgent,
	"admin": ProfileAdmin,
}

// ResolveTools takes a comma-separated string of profile names and/or
// individual tool names and returns the set of tool names to register.
// An empty input means "all" — every tool is registered.
func ResolveTools(input string) map[string]bool {
	input = strings.TrimSpace(input)
	if input == "" || input == "all" {
		return nil // nil means register everything
	}

	result := make(map[string]bool)
	for _, token := range strings.Split(input, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "all" {
			return nil
		}
		if profile, ok := Profiles[token]; ok {
			for tool := range profile {
				result[tool] = true
			}
		} else {
			// Treat as individual tool name
			result[token] = true
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// NewServer creates an MCP server with ALL tools registered (backwards compatible).
func NewServer(s *store.Store) *server.MCPServer {
	return NewServerWithConfig(s, MCPConfig{}, nil)
}

// serverInstructions tells MCP clients when to use Ohara's tools.
// 6 core tools are eager (always in context). The rest are deferred
// and require ToolSearch to load.
const serverInstructions = `Ohara provides persistent memory that survives across sessions and compactions.

CORE TOOLS (always available — use without ToolSearch):
  mem_save — save decisions, bugs, discoveries, conventions PROACTIVELY (do not wait to be asked)
  mem_search — find past work, decisions, or context from previous sessions
  mem_context — get recent memory context via context pack (call at session start or after compaction)
  mem_session_summary — save end-of-session summary (MANDATORY before saying "done")
  mem_save_prompt — save user prompt for context
  mem_pack — build an explicit context pack from memory items (token-budget-aware)
  mem_prime — build structured prime context with Knowledge vs Episode tier separation

DEFERRED TOOLS (use ToolSearch when needed):
  mem_update, mem_suggest_topic_key, mem_session_start, mem_session_end,
  mem_stats, mem_delete, mem_timeline, mem_capture_passive, mem_merge_projects,
  mem_list_domains, mem_mark_used, mem_append_outcome, mem_resolve_conflict,
  mem_forget, mem_link, mem_unlink, mem_related, mem_consolidate_candidates,
  mem_mark_consolidated, mem_search_rerank, mem_feedback, mem_graph_context,
  mem_extract_entities

PROACTIVE SAVE RULE: Call mem_save immediately after ANY decision, bug fix, discovery, or convention — not just when asked.`

// NewServerWithTools creates an MCP server registering only the tools in
// the allowlist. If allowlist is nil, all tools are registered.
func NewServerWithTools(s *store.Store, allowlist map[string]bool) *server.MCPServer {
	return NewServerWithConfig(s, MCPConfig{}, allowlist)
}

// NewServerWithConfig creates an MCP server with full configuration including
// default project detection and optional tool allowlist.
func NewServerWithConfig(s *store.Store, cfg MCPConfig, allowlist map[string]bool) *server.MCPServer {
	return newServerWithActivity(s, cfg, allowlist, NewSessionActivity(10*time.Minute))
}

func newServerWithActivity(s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) *server.MCPServer {
	srv := server.NewMCPServer(
		"ohara",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
	)

	registerTools(srv, s, cfg, allowlist, activity)
	return srv
}

// shouldRegister returns true if the tool should be registered given the
// allowlist. If allowlist is nil, everything is allowed.
func shouldRegister(name string, allowlist map[string]bool) bool {
	if allowlist == nil {
		return true
	}
	return allowlist[name]
}

func registerTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	registerSearchTools(srv, s, cfg, allowlist, activity)
	registerWriteTools(srv, s, cfg, allowlist, activity)
	registerContextTools(srv, s, cfg, allowlist, activity)
	registerSessionTools(srv, s, cfg, allowlist, activity)
	registerAdminProjectTools(srv, s, cfg, allowlist, activity)
	registerGraphRelationTools(srv, s, cfg, allowlist, activity)
	registerGovernanceTools(srv, s, cfg, allowlist, activity)
}

func registerSearchTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	if shouldRegister("mem_search", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_search",
				mcp.WithDescription("Search your persistent memory across all sessions. Use this to find past decisions, bugs fixed, patterns used, files changed, or any context from previous coding sessions."),
				mcp.WithTitleAnnotation("Search Memory"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("Search query — natural language or keywords"),
				),
				mcp.WithString("type",
					mcp.Description("Filter by type: tool_use, file_change, command, file_read, search, manual, decision, architecture, bugfix, pattern"),
				),
				mcp.WithString("project",
					mcp.Description("Filter by project name"),
				),
				mcp.WithString("scope",
					mcp.Description("Filter by scope: project (default) or personal"),
				),
				mcp.WithString("domain",
					mcp.Description("Filter by domain/subsystem"),
				),
				mcp.WithString("written_by",
					mcp.Description("Filter by actor who wrote memory (user, agent, consolidation, import, system)"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Max results (default: 10, max: 20)"),
				),
				mcp.WithNumber("min_confidence",
					mcp.Description("Optional confidence threshold; abstains when top score is below this value"),
				),
			),
			handleSearch(s, cfg, activity),
		)
	}

	if shouldRegister("mem_search_rerank", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_search_rerank",
				mcp.WithDescription("Optional slow-path reranking on top of mem_search results. Uses model inference explicitly and is never called by default retrieval."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Search Rerank"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("query",
					mcp.Required(),
					mcp.Description("Search query to rerank results for"),
				),
				mcp.WithString("project",
					mcp.Description("Filter by project name"),
				),
				mcp.WithString("scope",
					mcp.Description("Filter by scope"),
				),
				mcp.WithString("type",
					mcp.Description("Filter by memory kind/type"),
				),
				mcp.WithString("domain",
					mcp.Description("Filter by domain"),
				),
				mcp.WithString("written_by",
					mcp.Description("Filter by writer actor"),
				),
				mcp.WithNumber("limit",
					mcp.Description("Initial search limit before reranking (default: 20)"),
				),
				mcp.WithNumber("top_n",
					mcp.Description("Number of top items to rerank (default: 8)"),
				),
			),
			handleSearchRerank(s, cfg, activity),
		)
	}
}
func registerWriteTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	if shouldRegister("mem_save", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_save",
				mcp.WithTitleAnnotation("Save Memory"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription(`Save an important observation to persistent memory. Call this PROACTIVELY after completing significant work — don't wait to be asked.

	WHEN to save (call this after each of these):
	- Architectural decisions or tradeoffs
	- Bug fixes (what was wrong, why, how you fixed it)
	- New patterns or conventions established
	- Configuration changes or environment setup
	- Important discoveries or gotchas
	- File structure changes

	FORMAT for content — use this structured format:
	  **What**: [concise description of what was done]
	  **Why**: [the reasoning, user request, or problem that drove it]
	  **Where**: [files/paths affected, e.g. src/auth/middleware.ts, internal/store/store.go]
	  **Learned**: [any gotchas, edge cases, or decisions made — omit if none]

	TITLE should be short and searchable, like: "JWT auth middleware", "FTS5 query sanitization", "Fixed N+1 in user list"

	Examples:
	  title: "Switched from sessions to JWT"
	  type: "decision"
	  content: "**What**: Replaced express-session with jsonwebtoken for auth\n**Why**: Session storage doesn't scale across multiple instances\n**Where**: src/middleware/auth.ts, src/routes/login.ts\n**Learned**: Must set httpOnly and secure flags on the cookie, refresh tokens need separate rotation logic"

	  title: "Fixed FTS5 syntax error on special chars"
	  type: "bugfix"
	  content: "**What**: Wrapped each search term in quotes before passing to FTS5 MATCH\n**Why**: Users typing queries like 'fix auth bug' would crash because FTS5 interprets special chars as operators\n**Where**: internal/store/store.go — sanitizeFTS() function\n**Learned**: FTS5 MATCH syntax is NOT the same as LIKE — always sanitize user input"`),
				mcp.WithString("title",
					mcp.Required(),
					mcp.Description("Short, searchable title (e.g. 'JWT auth middleware', 'Fixed N+1 query')"),
				),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("Structured content using **What**, **Why**, **Where**, **Learned** format"),
				),
				mcp.WithString("type",
					mcp.Description("Category: decision, architecture, bugfix, pattern, config, discovery, learning (default: manual)"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID to associate with (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Description("Project name"),
				),
				mcp.WithString("scope",
					mcp.Description("Scope for this observation: project (default) or personal"),
				),
				mcp.WithString("topic_key",
					mcp.Description("Optional topic identifier for upserts (e.g. architecture/auth-model). Reuses and updates the latest observation in same project+scope."),
				),
				mcp.WithString("domain",
					mcp.Description("Subsystem or domain this memory applies to (e.g. auth, database, api)"),
				),
				mcp.WithString("classification",
					mcp.Description("Classification tier: foundational (never expires), tactical (medium), observational (short-lived)"),
				),
				mcp.WithString("written_by",
					mcp.Description("Actor who created this memory (e.g. user, agent, consolidation, import)"),
				),
				mcp.WithString("expires_at",
					mcp.Description("ISO timestamp when this memory should auto-expire and be archived"),
				),
				mcp.WithString("trigger",
					mcp.Description("When-X-happens trigger condition for procedure memories"),
				),
				mcp.WithString("evidence",
					mcp.Description("JSON object with commit, issue, file, or url evidence fields"),
				),
				mcp.WithString("applies_to",
					mcp.Description("JSON array of affected components or paths"),
				),
				mcp.WithString("related",
					mcp.Description("JSON array of related memory IDs"),
				),
				mcp.WithBoolean("force",
					mcp.Description("Bypass governance checks (e.g. missing evidence for decision/procedure)"),
				),
			),
			handleSave(s, cfg, activity),
		)
	}

	if shouldRegister("mem_update", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_update",
				mcp.WithDescription("Update an existing observation by ID. Only provided fields are changed."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Update Memory"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("id",
					mcp.Required(),
					mcp.Description("Observation ID to update"),
				),
				mcp.WithString("title",
					mcp.Description("New title"),
				),
				mcp.WithString("content",
					mcp.Description("New content"),
				),
				mcp.WithString("type",
					mcp.Description("New type/category"),
				),
				mcp.WithString("project",
					mcp.Description("New project value"),
				),
				mcp.WithString("scope",
					mcp.Description("New scope: project or personal"),
				),
				mcp.WithString("topic_key",
					mcp.Description("New topic key (normalized internally)"),
				),
			),
			handleUpdate(s),
		)
	}

	if shouldRegister("mem_delete", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_delete",
				mcp.WithDescription("Delete an observation by ID. Soft-delete by default; set hard_delete=true for permanent deletion."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Delete Memory"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("id",
					mcp.Required(),
					mcp.Description("Observation ID to delete"),
				),
				mcp.WithBoolean("hard_delete",
					mcp.Description("If true, permanently deletes the observation"),
				),
			),
			handleDelete(s),
		)
	}

	if shouldRegister("mem_save_prompt", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_save_prompt",
				mcp.WithDescription("Save a user prompt to persistent memory. Use this to record what the user asked — their intent, questions, and requests — so future sessions have context about the user's goals."),
				mcp.WithTitleAnnotation("Save User Prompt"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("The user's prompt text"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID to associate with (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Description("Project name"),
				),
			),
			handleSavePrompt(s, cfg),
		)
	}
}
func registerContextTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	if shouldRegister("mem_suggest_topic_key", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_suggest_topic_key",
				mcp.WithDescription("Suggest a stable topic_key for memory upserts. Use this before mem_save when you want evolving topics (like architecture decisions) to update a single observation over time."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Suggest Topic Key"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("type",
					mcp.Description("Observation type/category, e.g. architecture, decision, bugfix"),
				),
				mcp.WithString("title",
					mcp.Description("Observation title (preferred input for stable keys)"),
				),
				mcp.WithString("content",
					mcp.Description("Observation content used as fallback if title is empty"),
				),
			),
			handleSuggestTopicKey(),
		)
	}

	if shouldRegister("mem_context", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_context",
				mcp.WithDescription("Get recent memory context from previous sessions via a token-budget-aware context pack. Uses the memory_items foundation to assemble global + project memories within a token budget (default: 400 tokens). Shows recent sessions and memories to understand what was done before."),
				mcp.WithTitleAnnotation("Get Memory Context"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("project",
					mcp.Description("Filter by project (omit for all projects)"),
				),
				mcp.WithString("domain",
					mcp.Description("Optional domain/subsystem filter"),
				),
				mcp.WithString("asof",
					mcp.Description("Optional as-of timestamp filter (RFC3339)"),
				),
				mcp.WithNumber("budget_tokens",
					mcp.Description("Token budget for the context pack (default: 400, max: 800)"),
				),
			),
			handleContext(s, cfg, activity),
		)
	}

	if shouldRegister("mem_stats", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_stats",
				mcp.WithDescription("Show memory system statistics — total sessions, memories, prompts, and memory items tracked (by kind, scope, and status)."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Memory Stats"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
			),
			handleStats(s),
		)
	}

	if shouldRegister("mem_pack", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_pack",
				mcp.WithDescription("Build a token-budget-aware context pack from memory items within a token budget."),
				mcp.WithTitleAnnotation("Build Memory Pack"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("project_id",
					mcp.Required(),
					mcp.Description("Project ID to build context pack for"),
				),
				mcp.WithString("session_id",
					mcp.Description("Optional session ID — includes postmortems from that session in the pack"),
				),
				mcp.WithNumber("budget_tokens",
					mcp.Description("Token budget for the pack (default: 400, max: 800)"),
				),
			),
			handlePack(s),
		)
	}

	if shouldRegister("mem_timeline", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_timeline",
				mcp.WithDescription("Show chronological context around a specific memory item. Use after mem_search to drill into the timeline of events surrounding a search result."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Memory Timeline"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("memory_id",
					mcp.Required(),
					mcp.Description("The memory ID to center the timeline on (from mem_search results)"),
				),
				mcp.WithNumber("before",
					mcp.Description("Number of memories to show before the focus (default: 5)"),
				),
				mcp.WithNumber("after",
					mcp.Description("Number of memories to show after the focus (default: 5)"),
				),
			),
			handleTimeline(s),
		)
	}

	if shouldRegister("mem_prime", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_prime",
				mcp.WithDescription("Build a structured prime context pack with Knowledge vs Episode tier separation. Returns memory items organized by Decisions, Patterns, Known Failures, and Procedures sections."),
				mcp.WithTitleAnnotation("Prime Context"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("project_id",
					mcp.Required(),
					mcp.Description("Project ID to build prime context for"),
				),
				mcp.WithString("domain",
					mcp.Description("Filter by domain/namespace"),
				),
				mcp.WithNumber("budget_tokens",
					mcp.Description("Token budget for the prime context (default: 2000)"),
				),
				mcp.WithString("kinds",
					mcp.Description("Filter by memory kinds (comma-separated)"),
				),
				mcp.WithString("files",
					mcp.Description("Comma-separated file/path hints to bias matching applies_to_json"),
				),
				mcp.WithString("format",
					mcp.Description("Output format: md (default), xml, or json"),
				),
				mcp.WithBoolean("show_actor",
					mcp.Description("Include [written_by] actor tags in output entries"),
				),
			),
			handlePrime(s),
		)
	}
}
func registerSessionTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	if shouldRegister("mem_session_summary", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_summary",
				mcp.WithTitleAnnotation("Save Session Summary"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription(`Save a comprehensive end-of-session summary. Call this when a session is ending or when significant work is complete. This creates a structured summary that future sessions will use to understand what happened.

	FORMAT — use this exact structure in the content field:

	## Goal
	[One sentence: what were we building/working on in this session]

	## Instructions
	[User preferences, constraints, or context discovered during this session. Things a future agent needs to know about HOW the user wants things done. Skip if nothing notable.]

	## Discoveries
	- [Technical finding, gotcha, or learning 1]
	- [Technical finding 2]
	- [Important API behavior, config quirk, etc.]

	## Accomplished
	- ✅ [Completed task 1 — with key implementation details]
	- ✅ [Completed task 2 — mention files changed]
	- 🔲 [Identified but not yet done — for next session]

	## Relevant Files
	- path/to/file.ts — [what it does or what changed]
	- path/to/other.go — [role in the architecture]

	GUIDELINES:
	- Be CONCISE but don't lose important details (file paths, error messages, decisions)
	- Focus on WHAT and WHY, not HOW (the code itself is in the repo)
	- Include things that would save a future agent time
	- The Discoveries section is the most valuable — capture gotchas and non-obvious learnings
	- Relevant Files should only include files that were significantly changed or are important for context`),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("Full session summary using the Goal/Instructions/Discoveries/Accomplished/Files format"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Required(),
					mcp.Description("Project name"),
				),
			),
			handleSessionSummary(s, cfg, activity),
		)
	}

	if shouldRegister("mem_session_start", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_start",
				mcp.WithDescription("Register the start of a new coding session. Call this at the beginning of a session to track activity."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Start Session"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("id",
					mcp.Required(),
					mcp.Description("Unique session identifier"),
				),
				mcp.WithString("project",
					mcp.Required(),
					mcp.Description("Project name"),
				),
				mcp.WithString("directory",
					mcp.Description("Working directory"),
				),
			),
			handleSessionStart(s, cfg, activity),
		)
	}

	if shouldRegister("mem_session_end", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_end",
				mcp.WithDescription("Mark a coding session as completed with an optional summary."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("End Session"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("id",
					mcp.Required(),
					mcp.Description("Session identifier to close"),
				),
				mcp.WithString("summary",
					mcp.Description("Summary of what was accomplished"),
				),
				mcp.WithString("project",
					mcp.Description("Project name (used to clear activity tracking)"),
				),
			),
			handleSessionEnd(s, cfg, activity),
		)
	}

	if shouldRegister("mem_capture_passive", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_capture_passive",
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Capture Learnings"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithDescription(`Extract and save structured learnings from text output. Use this at the end of a task to capture knowledge automatically.

	The tool looks for sections like "## Key Learnings:" or "## Aprendizajes Clave:" and extracts numbered or bulleted items. Each item is saved as a separate observation.

	Duplicates are automatically detected and skipped — safe to call multiple times with the same content.`),
				mcp.WithString("content",
					mcp.Required(),
					mcp.Description("The text output containing a '## Key Learnings:' section with numbered or bulleted items"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID (default: manual-save-{project})"),
				),
				mcp.WithString("project",
					mcp.Description("Project name"),
				),
				mcp.WithString("source",
					mcp.Description("Source identifier (e.g. 'subagent-stop', 'session-end')"),
				),
			),
			handleCapturePassive(s, cfg, activity),
		)
	}
}
func registerAdminProjectTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	if shouldRegister("mem_merge_projects", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_merge_projects",
				mcp.WithDescription("Merge memories from multiple project name variants into one canonical name. Use when you discover project name drift (e.g. 'Ohara' and 'ohara' should be the same project). DESTRUCTIVE — moves all records from source names to the canonical name."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Merge Projects"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("from",
					mcp.Required(),
					mcp.Description("Comma-separated list of project names to merge FROM (e.g. 'ohara,ohara-memory,OHARA')"),
				),
				mcp.WithString("to",
					mcp.Required(),
					mcp.Description("The canonical project name to merge INTO (e.g. 'ohara')"),
				),
			),
			handleMergeProjects(s),
		)
	}

	if shouldRegister("mem_list_domains", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_list_domains",
				mcp.WithDescription("List all distinct domains for a project from memory items."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("List Domains"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("project_id",
					mcp.Required(),
					mcp.Description("Project ID to list domains for"),
				),
			),
			handleListDomains(s),
		)
	}
}
func registerGraphRelationTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	if shouldRegister("mem_link", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_link",
				mcp.WithDescription("Create a typed relation between two memories. Relation types: caused, resolves, supersedes, related_to, implements, contradicts."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Link Memories"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("from_obs_id",
					mcp.Required(),
					mcp.Description("Source memory ID"),
				),
				mcp.WithNumber("to_obs_id",
					mcp.Required(),
					mcp.Description("Target memory ID"),
				),
				mcp.WithString("relation",
					mcp.Required(),
					mcp.Description("Relation type: caused, resolves, supersedes, related_to, implements, contradicts"),
				),
			),
			handleLink(s),
		)
	}

	if shouldRegister("mem_unlink", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_unlink",
				mcp.WithDescription("Remove a typed relation between two memories."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Unlink Memories"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("from_obs_id",
					mcp.Required(),
					mcp.Description("Source memory ID"),
				),
				mcp.WithNumber("to_obs_id",
					mcp.Required(),
					mcp.Description("Target memory ID"),
				),
				mcp.WithString("relation",
					mcp.Required(),
					mcp.Description("Relation type to remove"),
				),
			),
			handleUnlink(s),
		)
	}

	if shouldRegister("mem_related", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_related",
				mcp.WithDescription("Traverse relations from a given memory. Returns related memories."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Related Memories"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("obs_id",
					mcp.Required(),
					mcp.Description("Memory ID to traverse relations from"),
				),
				mcp.WithString("relation",
					mcp.Description("Filter by relation type (optional)"),
				),
			),
			handleRelated(s),
		)
	}

	// ─── mem_extract_entities (profile: agent, deferred) ─────────────────
	if shouldRegister("mem_extract_entities", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_extract_entities",
				mcp.WithDescription("Extract entities from a memory and link them into the optional graph index."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Extract Entities"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("obs_id", mcp.Required(), mcp.Description("Memory ID to extract entities from")),
				mcp.WithString("project", mcp.Description("Project key override (default: memory project)")),
			),
			handleExtractEntities(s),
		)
	}

	// ─── mem_graph_context (profile: agent, deferred) ────────────────────
	if shouldRegister("mem_graph_context", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_graph_context",
				mcp.WithDescription("Entity-centric graph context retrieval from entities -> linked memories."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Graph Context"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("entity", mcp.Required(), mcp.Description("Entity name to pivot graph retrieval on")),
				mcp.WithString("project", mcp.Description("Project scope")),
				mcp.WithNumber("limit", mcp.Description("Max linked memories to return (default: 10)")),
			),
			handleGraphContext(s),
		)
	}
}
func registerGovernanceTools(srv *server.MCPServer, s *store.Store, cfg MCPConfig, allowlist map[string]bool, activity *SessionActivity) {
	if shouldRegister("mem_mark_used", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_mark_used",
				mcp.WithDescription("Record that a memory item was retrieved or used. Increments access_count and updates last_accessed timestamp."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Mark Memory Used"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("memory_id",
					mcp.Required(),
					mcp.Description("Memory ID (number, or pass as string for array)"),
				),
				mcp.WithString("event",
					mcp.Description("Event type: 'retrieved' (default) or 'used'"),
				),
				mcp.WithString("session_id",
					mcp.Description("Session ID associated with this usage"),
				),
			),
			handleMarkUsed(s),
		)
	}

	if shouldRegister("mem_append_outcome", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_append_outcome",
				mcp.WithDescription("Append an outcome record (success/failure/unknown) to a memory item."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Append Outcome"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("memory_id",
					mcp.Required(),
					mcp.Description("Memory ID to append outcome to"),
				),
				mcp.WithString("status",
					mcp.Required(),
					mcp.Description("Outcome status: 'success', 'failure', or 'unknown'"),
				),
				mcp.WithString("notes",
					mcp.Description("Optional notes about the outcome"),
				),
				mcp.WithString("actor_id",
					mcp.Description("Actor who recorded the outcome (default: agent)"),
				),
			),
			handleAppendOutcome(s),
		)
	}

	if shouldRegister("mem_resolve_conflict", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_resolve_conflict",
				mcp.WithDescription("Resolve a detected memory conflict via merge, link, or suppress action."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Resolve Conflict"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("obs_id_a",
					mcp.Required(),
					mcp.Description("First memory ID in the conflict pair"),
				),
				mcp.WithNumber("obs_id_b",
					mcp.Required(),
					mcp.Description("Second memory ID in the conflict pair"),
				),
				mcp.WithString("action",
					mcp.Required(),
					mcp.Description("Resolution action: 'add' (both memories coexist), 'merge' (create new memory, supersede both), 'invalidate' (expire older), 'relate' (add relation via relation_type), or 'suppress' (record suppression)"),
				),
				mcp.WithString("merged_content",
					mcp.Description("Required for 'merge' action: the content of the merged memory"),
				),
				mcp.WithString("relation_type",
					mcp.Description("Required for 'relate' action: type of relation (e.g., 'supersedes', 'contradicts', 'refines')"),
				),
			),
			handleResolveConflict(s),
		)
	}

	if shouldRegister("mem_forget", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_forget",
				mcp.WithDescription("Archive a memory with a documented reason; preserves audit trail"),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Forget Memory"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("obs_id",
					mcp.Required(),
					mcp.Description("Memory ID to archive"),
				),
				mcp.WithString("reason",
					mcp.Required(),
					mcp.Description("Reason for forgetting"),
				),
				mcp.WithNumber("replacement_obs_id",
					mcp.Description("ID of the memory that supersedes this one"),
				),
			),
			handleForget(s),
		)
	}

	if shouldRegister("mem_consolidate_candidates", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_consolidate_candidates",
				mcp.WithDescription("Returns grouped episodic source memories for consolidation review. The calling agent should synthesize a semantic summary, save it with mem_save using source='consolidation', then call mem_mark_consolidated."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Consolidation Candidates"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithString("project",
					mcp.Description("Filter by project name"),
				),
				mcp.WithString("domain",
					mcp.Description("Filter by domain"),
				),
			),
			handleConsolidationCandidates(s),
		)
	}

	// ─── mem_mark_consolidated (profile: agent, deferred) ──────────────────
	if shouldRegister("mem_mark_consolidated", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_mark_consolidated",
				mcp.WithDescription("Archives a reviewed consolidation candidate and its source episodic memories after a semantic consolidation memory has already been saved."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Mark Consolidated"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("id",
					mcp.Required(),
					mcp.Description("ID of the candidate memory being marked as consolidated"),
				),
				mcp.WithNumber("consolidated_memory_id",
					mcp.Required(),
					mcp.Description("ID of the semantic memory already saved with source='consolidation'"),
				),
			),
			handleMarkConsolidated(s),
		)
	}

	// ─── mem_feedback (profile: agent, deferred) ─────────────────────────
	if shouldRegister("mem_feedback", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_feedback",
				mcp.WithDescription("Record explicit utility feedback for a memory and adjust utility_weight."),
				mcp.WithDeferLoading(true),
				mcp.WithTitleAnnotation("Memory Feedback"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
				mcp.WithNumber("obs_id", mcp.Required(), mcp.Description("Memory ID to rate")),
				mcp.WithNumber("reward", mcp.Required(), mcp.Description("Reward in [-1.0, 1.0]")),
				mcp.WithString("notes", mcp.Description("Optional feedback notes")),
				mcp.WithString("actor_id", mcp.Description("Actor recording feedback (default: agent)")),
			),
			handleFeedback(s),
		)
	}
}

// ─── Tool Handlers ───────────────────────────────────────────────────────────

func handleSearch(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.GetArguments()["query"].(string)
		typ, _ := req.GetArguments()["type"].(string)
		project, _ := req.GetArguments()["project"].(string)
		scope, _ := req.GetArguments()["scope"].(string)
		domain, _ := req.GetArguments()["domain"].(string)
		writtenBy, _ := req.GetArguments()["written_by"].(string)
		limit := intArg(req, "limit", 10)
		minConfidence := floatArg(req, "min_confidence", 0.0)

		// Apply default project when LLM sends empty
		if project == "" {
			project = cfg.DefaultProject
		}
		// Normalize project name
		project, _ = store.NormalizeProject(project)

		sessionID := defaultSessionID(project)
		activity.RecordToolCall(sessionID)

		memItems, err := s.SearchMemories(query, project, scope, typ, domain, store.MemoryStatusActive, limit, writtenBy)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search error: %s. Try simpler keywords.", err)), nil
		}

		hasMem := len(memItems) > 0

		if !hasMem {
			return mcp.NewToolResultText(fmt.Sprintf("No memories found for: %q", query)), nil
		}

		// min_confidence abstention
		if minConfidence > 0 && len(memItems) > 0 {
			topScore := memItems[0].RelevanceScore
			if topScore < minConfidence {
				payload := map[string]any{
					"memories":       []any{},
					"low_confidence": true,
					"message": fmt.Sprintf("No memories met the minimum confidence threshold of %.2f for query %q.",
						minConfidence, query),
					"top_score": topScore,
				}
				data, _ := json.MarshalIndent(payload, "", "  ")
				return mcp.NewToolResultText(string(data)), nil
			}
		}

		// Retrieval-time conflict surfacing: check if any results have been superseded
		type conflictEntry struct {
			SupersededID     int64  `json:"superseded_id"`
			SupersededTitle  string `json:"superseded_title"`
			SupersedingID    int64  `json:"superseding_id"`
			SupersedingTitle string `json:"superseding_title"`
		}
		var detectedConflicts []conflictEntry
		for _, m := range memItems {
			if m.SupersededBy != nil && *m.SupersededBy != 0 {
				supMem, _ := s.GetMemory(*m.SupersededBy)
				if supMem != nil && supMem.Status == store.MemoryStatusActive {
					detectedConflicts = append(detectedConflicts, conflictEntry{
						SupersededID:     m.ID,
						SupersededTitle:  m.Title,
						SupersedingID:    *m.SupersededBy,
						SupersedingTitle: supMem.Title,
					})
				}
			}
		}

		var b strings.Builder

		// Memory items results
		if hasMem {
			fmt.Fprintf(&b, "Found %d memory item(s):\n\n", len(memItems))
			for i, m := range memItems {
				preview := util.Truncate(m.Body, 300)
				if len(m.Body) > 300 {
					preview += " [preview]"
				}
				supsersededNote := ""
				if m.Status == store.MemoryStatusSuperseded {
					supsersededNote = " [superseded]"
				}
				fmt.Fprintf(&b, "[%d] **%s** (%s | %s | %s)%s\n    %s\n    %s | tokens: ~%d\n\n",
					i+1, m.Title, m.Kind, m.Scope, m.Source,
					supsersededNote,
					preview,
					m.UpdatedAt,
					estimateTokens(m.Body),
				)
			}
		}

		// Append conflict warnings if any found
		if len(detectedConflicts) > 0 {
			conflictJSON, _ := json.Marshal(map[string]interface{}{
				"conflicts": detectedConflicts,
			})
			fmt.Fprintf(&b, "\n---\nConflicts detected (JSON):\n```json\n%s\n```\n", string(conflictJSON))
		}

		if nudge := activity.NudgeIfNeeded(sessionID); nudge != "" {
			b.WriteString(nudge)
		}

		return mcp.NewToolResultText(b.String()), nil
	}
}

func handleSearchRerank(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.GetArguments()["query"].(string)
		if strings.TrimSpace(query) == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		project, _ := req.GetArguments()["project"].(string)
		scope, _ := req.GetArguments()["scope"].(string)
		typ, _ := req.GetArguments()["type"].(string)
		domain, _ := req.GetArguments()["domain"].(string)
		writtenBy, _ := req.GetArguments()["written_by"].(string)
		limit := intArg(req, "limit", 20)
		topN := intArg(req, "top_n", 8)

		if project == "" {
			project = cfg.DefaultProject
		}
		project, _ = store.NormalizeProject(project)
		sessionID := defaultSessionID(project)
		activity.RecordToolCall(sessionID)

		items, err := s.SearchMemories(query, project, scope, typ, domain, store.MemoryStatusActive, limit, writtenBy)
		if err != nil {
			return mcp.NewToolResultError("Failed to search memories: " + err.Error()), nil
		}
		if len(items) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No memories found for: %q", query)), nil
		}
		reranked, err := s.RerankMemoriesWithLLM(query, items, topN)
		if err != nil {
			return mcp.NewToolResultError("Rerank failed: " + err.Error()), nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Reranked %d results (top_n=%d):\n\n", len(reranked), topN)
		for i, item := range reranked {
			preview := util.Truncate(item.Body, 220)
			if len(item.Body) > 220 {
				preview += " [preview]"
			}
			fmt.Fprintf(&b, "[%d] #%d **%s** (%s | %s | score=%.3f)\n    %s\n\n",
				i+1, item.ID, item.Title, item.Kind, item.Scope, item.RelevanceScore, preview)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

// normalizeKindForSave maps legacy or invalid kind strings to valid memory kinds.
// This prevents invalid kinds like "manual"/"note"/"architecture" from reaching store.AddMemory.
func normalizeKindForSave(kind string) string {
	switch strings.ToLower(kind) {
	case "manual", "note", "learning":
		return store.MemoryKindDiscovery
	case "architecture":
		return store.MemoryKindDecision
	case "session_summary":
		return store.MemoryKindPostmortem
	default:
		if store.ValidMemoryKinds[kind] {
			return kind
		}
		return store.MemoryKindDiscovery
	}
}

func handleSave(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title, _ := req.GetArguments()["title"].(string)
		content, _ := req.GetArguments()["content"].(string)
		typ, _ := req.GetArguments()["type"].(string)
		sessionID, _ := req.GetArguments()["session_id"].(string)
		project, _ := req.GetArguments()["project"].(string)
		scope, _ := req.GetArguments()["scope"].(string)
		topicKey, _ := req.GetArguments()["topic_key"].(string)
		domain, _ := req.GetArguments()["domain"].(string)
		classification, _ := req.GetArguments()["classification"].(string)
		writtenBy, _ := req.GetArguments()["written_by"].(string)
		expiresAt, _ := req.GetArguments()["expires_at"].(string)
		trigger, _ := req.GetArguments()["trigger"].(string)
		evidence, _ := req.GetArguments()["evidence"].(string)
		appliesTo, _ := req.GetArguments()["applies_to"].(string)
		related, _ := req.GetArguments()["related"].(string)
		force := boolArg(req, "force", false)

		// Apply default project when LLM sends empty
		if project == "" {
			project = cfg.DefaultProject
		}
		// Normalize project name and capture warning
		normalized, normWarning := store.NormalizeProject(project)
		project = normalized

		// Normalize kind to a valid memory kind before storage
		typ = normalizeKindForSave(typ)
		if sessionID == "" {
			sessionID = defaultSessionID(project)
		}

		var governanceWarning string
		if !force && (typ == store.MemoryKindDecision || typ == store.MemoryKindProcedure) {
			ev := strings.TrimSpace(evidence)
			if ev == "" || ev == "{}" {
				governanceWarning = "⚠ Missing evidence for decision/procedure memory. Add commit/issue/file evidence, or use force=true when intentional."
			}
		}
		suggestedTopicKey := suggestTopicKey(typ, title, content)

		// Check for similar existing projects (only when this project has no existing memories)
		var similarWarning string
		if project != "" {
			existingNames, _ := s.ListProjectNames()
			isNew := true
			for _, e := range existingNames {
				if e == project {
					isNew = false
					break
				}
			}
			if isNew && len(existingNames) > 0 {
				matches := projectpkg.FindSimilar(project, existingNames, 3)
				if len(matches) > 0 {
					bestMatch := matches[0].Name
					similarWarning = fmt.Sprintf("⚠️ Project %q has no memories. Similar project found: %q. Consider using that name instead.", project, bestMatch)
				}
			}
		}

		// Conflict detection for decision/pattern/config kinds — surfaces warning without blocking save.
		// Mirrors the server's /memories endpoint behavior so MCP and HTTP paths are aligned.
		var conflictWarning string
		var conflict *store.ConflictInfo
		var err error
		if typ == store.MemoryKindDecision || typ == store.MemoryKindPattern || typ == store.MemoryKindConfig {
			conflict, err = s.DetectConflict(store.AddMemoryParams{
				ProjectID: project,
				Kind:      typ,
				Title:     title,
				Body:      content,
			})
			if err != nil {
				// Non-fatal: log and continue with save
				conflictWarning = ""
			} else if conflict != nil {
				conflictWarning = fmt.Sprintf("⚠️ Conflict detected: %s (similarity: %.0f%%). Consider using mem_update to revise the existing memory instead of creating a duplicate.", conflict.Message, conflict.OverlapScore*100)
			}
		}

		// Ensure the session exists
		s.CreateSession(sessionID, project, "")

		memID, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:        project,
			Kind:             typ,
			Scope:            scope,
			Title:            title,
			Body:             content,
			Source:           "mcp",
			ActorID:          "agent",
			SessionID:        sessionID,
			Domain:           domain,
			Classification:   classification,
			WrittenBy:        writtenBy,
			ExpiresAt:        expiresAt,
			TriggerCondition: trigger,
			EvidenceJSON:     evidence,
			AppliesToJSON:    appliesTo,
			RelatedJSON:      related,
			TrustLevel:       "system",
		})
		if err != nil {
			return mcp.NewToolResultError("Failed to save: " + err.Error()), nil
		}

		activity.RecordSave(defaultSessionID(project))

		msg := fmt.Sprintf("Memory saved: %q (%s)", title, typ)

		// Auto-create contradicts relation when conflict was detected
		if conflict != nil && memID > 0 {
			if conflict.ExistingMemory != nil && conflict.ExistingMemory.ID > 0 {
				_ = s.AddRelation(memID, conflict.ExistingMemory.ID, "contradicts")
			}
		}
		if topicKey == "" && suggestedTopicKey != "" {
			msg += fmt.Sprintf("\nSuggested topic_key: %s", suggestedTopicKey)
		}
		if normWarning != "" {
			msg += "\n" + normWarning
		}
		if similarWarning != "" {
			msg += "\n" + similarWarning
		}
		if conflictWarning != "" {
			msg += "\n" + conflictWarning
		}
		if governanceWarning != "" {
			msg += "\n" + governanceWarning
		}
		return mcp.NewToolResultText(msg), nil
	}
}

func handleSuggestTopicKey() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		typ, _ := req.GetArguments()["type"].(string)
		title, _ := req.GetArguments()["title"].(string)
		content, _ := req.GetArguments()["content"].(string)

		if strings.TrimSpace(title) == "" && strings.TrimSpace(content) == "" {
			return mcp.NewToolResultError("provide title or content to suggest a topic_key"), nil
		}

		topicKey := suggestTopicKey(typ, title, content)
		if topicKey == "" {
			return mcp.NewToolResultError("could not suggest topic_key from input"), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Suggested topic_key: %s", topicKey)), nil
	}
}

func handleUpdate(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(intArg(req, "id", 0))
		if id == 0 {
			return mcp.NewToolResultError("id is required"), nil
		}

		update := store.UpdateMemoryParams{}
		if v, ok := req.GetArguments()["title"].(string); ok {
			update.Title = &v
		}
		if v, ok := req.GetArguments()["content"].(string); ok {
			update.Body = &v
		}
		if v, ok := req.GetArguments()["topic_key"].(string); ok {
			update.TriggerCondition = &v
		}
		if v, ok := req.GetArguments()["status"].(string); ok {
			update.Status = &v
		}

		if update.Title == nil && update.Body == nil && update.TriggerCondition == nil && update.Status == nil {
			return mcp.NewToolResultError("provide at least one field to update"), nil
		}

		mem, err := s.UpdateMemory(id, update)
		if err != nil {
			return mcp.NewToolResultError("Failed to update memory: " + err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Memory updated: #%d %q (%s)", mem.ID, mem.Title, mem.Kind)), nil
	}
}

func handleDelete(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(intArg(req, "id", 0))
		if id == 0 {
			return mcp.NewToolResultError("id is required"), nil
		}

		if err := s.ForgetMemory(id, "deleted via mem_delete", "agent"); err != nil {
			return mcp.NewToolResultError("Failed to delete memory: " + err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Memory #%d deleted", id)), nil
	}
}

func handleSavePrompt(s *store.Store, cfg MCPConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content, _ := req.GetArguments()["content"].(string)
		sessionID, _ := req.GetArguments()["session_id"].(string)
		project, _ := req.GetArguments()["project"].(string)

		// Apply default project when LLM sends empty
		if project == "" {
			project = cfg.DefaultProject
		}
		project, _ = store.NormalizeProject(project)

		if sessionID == "" {
			sessionID = defaultSessionID(project)
		}

		// Ensure the session exists
		s.CreateSession(sessionID, project, "")

		_, err := s.AddPrompt(store.AddPromptParams{
			SessionID: sessionID,
			Content:   content,
			Project:   project,
		})
		if err != nil {
			return mcp.NewToolResultError("Failed to save prompt: " + err.Error()), nil
		}

		guidance := "Guidance: include domain when known; set classification for strong signals; add evidence for decision/procedure; set expires_at for temporary facts; set trigger for procedure memories."
		return mcp.NewToolResultText(fmt.Sprintf("Prompt saved: %q\n%s", util.Truncate(content, 80), guidance)), nil
	}
}

func handleContext(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project, _ := req.GetArguments()["project"].(string)
		domain, _ := req.GetArguments()["domain"].(string)
		asof, _ := req.GetArguments()["asof"].(string)

		// Apply default project when LLM sends empty
		if project == "" {
			project = cfg.DefaultProject
		}
		project, _ = store.NormalizeProject(project)

		sessionID := defaultSessionID(project)
		activity.RecordToolCall(sessionID)

		budgetTokens := intArg(req, "budget_tokens", 400)
		if budgetTokens > 800 {
			budgetTokens = 800
		}

		// Use BuildPack (memory_items foundation) for token-budget-aware context
		packResult, err := s.BuildPack(store.PackParams{
			ProjectID:    project,
			BudgetTokens: budgetTokens,
			Domain:       domain,
			Asof:         asof,
		})
		if err != nil {
			return mcp.NewToolResultError("Failed to get context: " + err.Error()), nil
		}

		packText := store.FormatPackText(packResult)
		if packText == "" || packText == "No memory context available." {
			return mcp.NewToolResultText("No previous session memories found."), nil
		}

		// Augment with legacy stats for backwards-compatible context
		stats, _ := s.Stats()
		var projects string
		if len(stats.Projects) > 0 {
			projects = strings.Join(stats.Projects, ", ")
		} else {
			projects = "none"
		}

		result := fmt.Sprintf("%s\n---\nMemory stats: %d sessions, %d memories across projects: %s",
			packText, stats.TotalSessions, stats.TotalMemories, projects)

		type ctxConflictEntry struct {
			SupersededID     int64  `json:"superseded_id"`
			SupersededTitle  string `json:"superseded_title"`
			SupersedingID    int64  `json:"superseding_id"`
			SupersedingTitle string `json:"superseding_title"`
		}
		var ctxConflicts []ctxConflictEntry
		for _, m := range packResult.MemoryItems {
			if m.SupersededBy != nil && *m.SupersededBy != 0 {
				supMem, _ := s.GetMemory(*m.SupersededBy)
				if supMem != nil && supMem.Status == store.MemoryStatusActive {
					ctxConflicts = append(ctxConflicts, ctxConflictEntry{
						SupersededID:     m.ID,
						SupersededTitle:  m.Title,
						SupersedingID:    *m.SupersededBy,
						SupersedingTitle: supMem.Title,
					})
				}
			}
		}
		if len(ctxConflicts) > 0 {
			cj, _ := json.Marshal(map[string]interface{}{
				"conflicts": ctxConflicts,
			})
			result += fmt.Sprintf("\n\nConflicts detected (JSON):\n```json\n%s\n```\n", string(cj))
		}

		if nudge := activity.NudgeIfNeeded(sessionID); nudge != "" {
			result += nudge
		}

		return mcp.NewToolResultText(result), nil
	}
}

func handleStats(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		stats, memStats, err := loadMCPStatsCombined(s)
		if err != nil {
			return mcp.NewToolResultError("Failed to get stats: " + err.Error()), nil
		}

		var projects string
		if len(stats.Projects) > 0 {
			projects = strings.Join(stats.Projects, ", ")
		} else {
			projects = "none yet"
		}

		result := fmt.Sprintf("Memory System Stats:\n- Sessions: %d\n- Memories: %d\n- Prompts: %d\n- Projects: %s",
			stats.TotalSessions, stats.TotalMemories, stats.TotalPrompts, projects)

		// Augment with memory_items stats if available
		if memStats != nil {
			result += fmt.Sprintf("\n- Memory items: %d (total)", memStats.TotalMemoryItems)
			if len(memStats.ByKind) > 0 {
				var kindParts []string
				for kind, count := range memStats.ByKind {
					kindParts = append(kindParts, fmt.Sprintf("%s=%d", kind, count))
				}
				result += fmt.Sprintf(" [by kind: %s]", strings.Join(kindParts, ", "))
			}
			if len(memStats.ByDomain) > 0 {
				var domainParts []string
				for domain, count := range memStats.ByDomain {
					domainParts = append(domainParts, fmt.Sprintf("%s=%d", domain, count))
				}
				result += fmt.Sprintf("\n- By domain: %s", strings.Join(domainParts, ", "))
			}
			if len(memStats.ByClassification) > 0 {
				var classParts []string
				for class, count := range memStats.ByClassification {
					classParts = append(classParts, fmt.Sprintf("%s=%d", class, count))
				}
				result += fmt.Sprintf("\n- By classification: %s", strings.Join(classParts, ", "))
			}
			if len(memStats.ByActor) > 0 {
				var actorParts []string
				for actor, count := range memStats.ByActor {
					actorParts = append(actorParts, fmt.Sprintf("%s=%d", actor, count))
				}
				result += fmt.Sprintf("\n- By actor: %s", strings.Join(actorParts, ", "))
			}
		}

		return mcp.NewToolResultText(result), nil
	}
}

func handlePack(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectID, _ := req.GetArguments()["project_id"].(string)
		if projectID == "" {
			return mcp.NewToolResultError("project_id is required"), nil
		}

		sessionID, _ := req.GetArguments()["session_id"].(string)
		budgetTokens := intArg(req, "budget_tokens", 400)

		result, err := s.BuildPack(store.PackParams{
			ProjectID:    projectID,
			SessionID:    sessionID,
			BudgetTokens: budgetTokens,
		})
		if err != nil {
			return mcp.NewToolResultError("Failed to build pack: " + err.Error()), nil
		}

		text := store.FormatPackText(result)
		return mcp.NewToolResultText(text), nil
	}
}

func handleTimeline(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		memoryID := int64(intArg(req, "memory_id", 0))
		if memoryID == 0 {
			return mcp.NewToolResultError("memory_id is required"), nil
		}
		count := intArg(req, "before", 5) + intArg(req, "after", 5)
		if count <= 0 {
			count = 10
		}

		result, err := s.MemoryTimeline(memoryID, count)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Timeline error: %s", err)), nil
		}

		var b strings.Builder

		fmt.Fprintf(&b, "Memory #%d (%s): %s\n\n", result.Anchor.ID, result.Anchor.Kind, result.Anchor.Title)
		fmt.Fprintf(&b, "%s\n\n", util.Truncate(result.Anchor.Body, 300))

		if len(result.Before) > 0 {
			b.WriteString("─── Before ───\n")
			for _, e := range result.Before {
				fmt.Fprintf(&b, "  #%d [%s] %s — %s\n", e.ID, e.Kind, e.Title, util.Truncate(e.Body, 150))
			}
			b.WriteString("\n")
		}

		if len(result.After) > 0 {
			b.WriteString("─── After ───\n")
			for _, e := range result.After {
				fmt.Fprintf(&b, "  #%d [%s] %s — %s\n", e.ID, e.Kind, e.Title, util.Truncate(e.Body, 150))
			}
		}

		return mcp.NewToolResultText(b.String()), nil
	}
}

func handleSessionSummary(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content, _ := req.GetArguments()["content"].(string)
		sessionID, _ := req.GetArguments()["session_id"].(string)
		project, _ := req.GetArguments()["project"].(string)

		// Apply default project when LLM sends empty
		if project == "" {
			project = cfg.DefaultProject
		}
		project, _ = store.NormalizeProject(project)

		if sessionID == "" {
			sessionID = defaultSessionID(project)
		}

		// Ensure the session exists
		s.CreateSession(sessionID, project, "")

		_, err := s.AddMemory(store.AddMemoryParams{
			SessionID:  sessionID,
			Kind:       store.MemoryKindPostmortem,
			Title:      fmt.Sprintf("Session summary: %s", project),
			Body:       content,
			ProjectID:  project,
			Source:     "mcp",
			ActorID:    "agent",
			TrustLevel: "system",
		})
		if err != nil {
			return mcp.NewToolResultError("Failed to save session summary: " + err.Error()), nil
		}

		msg := fmt.Sprintf("Session summary saved for project %q", project)
		if score := activity.ActivityScore(defaultSessionID(project)); score != "" {
			msg += "\n" + score
		}
		return mcp.NewToolResultText(msg), nil
	}
}

func handleSessionStart(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["id"].(string)
		project, _ := req.GetArguments()["project"].(string)
		directory, _ := req.GetArguments()["directory"].(string)

		// Apply default project when LLM sends empty
		if project == "" {
			project = cfg.DefaultProject
		}
		project, _ = store.NormalizeProject(project)

		activity.RecordToolCall(defaultSessionID(project))

		if err := s.CreateSession(id, project, directory); err != nil {
			return mcp.NewToolResultError("Failed to start session: " + err.Error()), nil
		}

		msg := fmt.Sprintf("Session %q started for project %q", id, project)

		// Check for consolidation candidates and append notice if any exist.
		candCount, _ := s.CountCandidates(project)
		if candCount > 0 {
			msg += fmt.Sprintf("\nNote: %d consolidation candidate(s) are ready for review. Call mem_consolidate_candidates to review them.", candCount)
		}

		return mcp.NewToolResultText(msg), nil
	}
}

func handleSessionEnd(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["id"].(string)
		summary, _ := req.GetArguments()["summary"].(string)

		if err := s.EndSession(id, summary); err != nil {
			return mcp.NewToolResultError("Failed to end session: " + err.Error()), nil
		}

		// Determine the project for this session to clean up activity tracking
		project := cfg.DefaultProject
		if p, _ := req.GetArguments()["project"].(string); p != "" {
			project = p
		}
		project, _ = store.NormalizeProject(project)
		activity.ClearSession(defaultSessionID(project))

		return mcp.NewToolResultText(fmt.Sprintf("Session %q completed", id)), nil
	}
}

func handleCapturePassive(s *store.Store, cfg MCPConfig, activity *SessionActivity) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content, _ := req.GetArguments()["content"].(string)
		sessionID, _ := req.GetArguments()["session_id"].(string)
		project, _ := req.GetArguments()["project"].(string)
		source, _ := req.GetArguments()["source"].(string)

		// Apply default project when LLM sends empty
		if project == "" {
			project = cfg.DefaultProject
		}
		project, _ = store.NormalizeProject(project)

		activity.RecordToolCall(defaultSessionID(project))

		if content == "" {
			return mcp.NewToolResultError("content is required — include text with a '## Key Learnings:' section"), nil
		}

		if sessionID == "" {
			sessionID = defaultSessionID(project)
			_ = s.CreateSession(sessionID, project, "")
		}

		if source == "" {
			source = "mcp-passive"
		}

		result, err := s.PassiveCapture(store.PassiveCaptureParams{
			SessionID: sessionID,
			Content:   content,
			Project:   project,
			Source:    source,
		})
		if err != nil {
			return mcp.NewToolResultError("Passive capture failed: " + err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Passive capture complete: extracted=%d saved=%d duplicates=%d",
			result.Extracted, result.Saved, result.Duplicates,
		)), nil
	}
}

func handleMergeProjects(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromStr, _ := req.GetArguments()["from"].(string)
		to, _ := req.GetArguments()["to"].(string)

		if fromStr == "" || to == "" {
			return mcp.NewToolResultError("both 'from' and 'to' are required"), nil
		}

		var sources []string
		for _, src := range strings.Split(fromStr, ",") {
			src = strings.TrimSpace(src)
			if src != "" {
				sources = append(sources, src)
			}
		}

		if len(sources) == 0 {
			return mcp.NewToolResultError("at least one source project name is required in 'from'"), nil
		}

		result, err := s.MergeProjects(sources, to)
		if err != nil {
			return mcp.NewToolResultError("Merge failed: " + err.Error()), nil
		}

		msg := fmt.Sprintf("Merged %d source(s) into %q:\n", len(result.SourcesMerged), result.Canonical)
		msg += fmt.Sprintf("  Observations moved: %d\n", result.ObservationsUpdated)
		msg += fmt.Sprintf("  Sessions moved:     %d\n", result.SessionsUpdated)
		msg += fmt.Sprintf("  Prompts moved:      %d\n", result.PromptsUpdated)

		return mcp.NewToolResultText(msg), nil
	}
}

// handleListDomains returns all distinct domains for a project.
func handleListDomains(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectID, _ := req.GetArguments()["project_id"].(string)

		rows, err := s.Query(
			`SELECT DISTINCT domain FROM memory_items
			 WHERE project_id = ? AND domain != '' AND status = 'active'
			 ORDER BY domain`,
			projectID,
		)
		if err != nil {
			return mcp.NewToolResultError("Failed to list domains: " + err.Error()), nil
		}
		defer rows.Close()

		var domains []string
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err == nil && d != "" {
				domains = append(domains, d)
			}
		}
		if err := rows.Err(); err != nil {
			return mcp.NewToolResultError("Failed to scan domains: " + err.Error()), nil
		}

		if len(domains) == 0 {
			return mcp.NewToolResultText("No domains found for this project."), nil
		}
		return mcp.NewToolResultText("Domains: " + strings.Join(domains, ", ")), nil
	}
}

// handlePrime builds a structured prime context pack with Knowledge vs Episode tier separation.
func handlePrime(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectID, _ := req.GetArguments()["project_id"].(string)
		domain, _ := req.GetArguments()["domain"].(string)
		budgetTokens := intArg(req, "budget_tokens", 2000)
		kindsStr, _ := req.GetArguments()["kinds"].(string)
		filesStr, _ := req.GetArguments()["files"].(string)
		format, _ := req.GetArguments()["format"].(string)
		if format == "" {
			format = "md"
		}
		showActor := boolArg(req, "show_actor", false)

		if projectID == "" {
			return mcp.NewToolResultError("project_id is required"), nil
		}

		// Parse kinds filter
		var kindFilter string
		if kindsStr != "" {
			kindFilter = kindsStr
		}

		// Query active memory items for project
		items, err := s.GetMemories(projectID, "", kindFilter, store.MemoryStatusActive, 100)
		if err != nil {
			return mcp.NewToolResultError("Failed to get memories: " + err.Error()), nil
		}

		// Filter by domain if specified
		if domain != "" {
			var filtered []store.MemoryItem
			for _, item := range items {
				if item.Domain == domain {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}

		// Optional file/path bias using applies_to_json match.
		if filesStr != "" {
			var hints []string
			for _, p := range strings.Split(filesStr, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					hints = append(hints, p)
				}
			}
			if len(hints) > 0 {
				score := func(it store.MemoryItem) int {
					m := 0
					for _, h := range hints {
						if strings.Contains(it.AppliesToJSON, h) {
							m++
						}
					}
					return m
				}
				sort.SliceStable(items, func(i, j int) bool {
					return score(items[i]) > score(items[j])
				})
			}
		}

		// Separate into Knowledge tier (foundational/tactical) and Episode tier (observational)
		var knowledge, episode []store.MemoryItem
		for _, item := range items {
			switch item.Classification {
			case "foundational", "tactical":
				knowledge = append(knowledge, item)
			case "observational":
				episode = append(episode, item)
			default:
				// Unclassified defaults to knowledge for prime purposes
				knowledge = append(knowledge, item)
			}
		}

		// Group by kind for structured output
		type section struct {
			title  string
			items  []store.MemoryItem
			header string
		}
		sections := []section{
			{title: "Decisions", header: "## Decisions\n"},
			{title: "Patterns", header: "## Patterns\n"},
			{title: "Known Failures", header: "## Known Failures\n"},
			{title: "Procedures", header: "## Procedures\n"},
		}
		kindToSection := map[string]int{
			store.MemoryKindDecision:  0,
			store.MemoryKindPattern:   1,
			store.MemoryKindBugfix:    2,
			store.MemoryKindProcedure: 3,
		}

		// formatSectionItem formats a single memory item as a string.
		formatSectionItem := func(item store.MemoryItem) string {
			tags := ""
			if len(item.Tags) > 0 {
				tags = " [" + strings.Join(item.Tags, ", ") + "]"
			}
			actorTag := ""
			if showActor && item.WrittenBy != "" {
				actorTag = " [" + item.WrittenBy + "]"
			}
			return fmt.Sprintf("**%s** (%s)%s%s\n%s", item.Title, item.Kind, tags, actorTag, item.Body)
		}

		buildSection := func(sec section) string {
			var sb strings.Builder
			sb.WriteString(sec.header)
			for _, item := range sec.items {
				section := formatSectionItem(item)
				if estimateTokens(sb.String())+estimateTokens(section) > budgetTokens {
					break
				}
				sb.WriteString(section)
				sb.WriteString("\n\n")
			}
			return sb.String()
		}

		var b strings.Builder

		// Build knowledge sections first (preserve Decisions and Patterns last)
		for _, item := range knowledge {
			idx := kindToSection[item.Kind]
			sections[idx].items = append(sections[idx].items, item)
		}

		// Append episode items to relevant sections
		for _, item := range episode {
			idx := kindToSection[item.Kind]
			sections[idx].items = append(sections[idx].items, item)
		}

		if format == "xml" {
			b.WriteString("<context>\n")
		} else if format == "json" {
			b.WriteString("{\n  \"knowledge\": {\n")
			first := true
			for _, sec := range sections {
				if len(sec.items) == 0 {
					continue
				}
				if !first {
					b.WriteString(",\n")
				}
				first = false
				b.WriteString(fmt.Sprintf("    \"%s\": [\n", sec.title))
				for i, item := range sec.items {
					if estimateTokens(b.String())+estimateTokens(item.Title) > budgetTokens {
						break
					}
					if i > 0 {
						b.WriteString(",\n")
					}
					b.WriteString(fmt.Sprintf("      {\"id\": %d, \"title\": %q, \"kind\": %q}",
						item.ID, item.Title, item.Kind))
				}
				b.WriteString("\n    ]")
			}
			b.WriteString("\n  }\n}\n")
			return mcp.NewToolResultText(b.String()), nil
		}

		// Markdown format (default)
		b.WriteString("## Knowledge Tier\n\n")
		for _, sec := range sections {
			if len(sec.items) == 0 {
				continue
			}
			content := buildSection(sec)
			if estimateTokens(b.String())+estimateTokens(content) > budgetTokens {
				break
			}
			b.WriteString(content)
			b.WriteString("\n")
		}

		return mcp.NewToolResultText(b.String()), nil
	}
}

// handleMarkUsed records memory usage events.
func handleMarkUsed(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		memoryID, _ := req.GetArguments()["memory_id"]
		event, _ := req.GetArguments()["event"].(string)
		sessionID, _ := req.GetArguments()["session_id"].(string)

		if event == "" {
			event = "retrieved"
		}
		if event != "retrieved" && event != "used" {
			return mcp.NewToolResultError("event must be 'retrieved' or 'used'"), nil
		}

		// Handle both single ID and array of IDs
		var ids []int64
		switch v := memoryID.(type) {
		case float64:
			ids = []int64{int64(v)}
		case string:
			if v != "" {
				if id, err := strconv.ParseInt(v, 10, 64); err == nil {
					ids = []int64{id}
				}
			}
		case []any:
			for _, item := range v {
				switch iv := item.(type) {
				case float64:
					ids = append(ids, int64(iv))
				case string:
					if id, err := strconv.ParseInt(iv, 10, 64); err == nil {
						ids = append(ids, id)
					}
				}
			}
		}

		if len(ids) == 0 {
			return mcp.NewToolResultError("memory_id is required"), nil
		}

		updated := 0
		now := time.Now().Format(time.RFC3339)
		for _, id := range ids {
			_, err := s.Exec(
				`INSERT INTO memory_usage (memory_id, event, session_id, ts) VALUES (?, ?, ?, ?)`,
				id, event, sessionID, now,
			)
			if err == nil {
				s.Exec(
					`UPDATE memory_items SET access_count = access_count + 1, last_accessed = ? WHERE id = ?`,
					now, id,
				)
				updated++
			}
		}

		return mcp.NewToolResultText(fmt.Sprintf("Recorded %s for %d memory item(s)", event, updated)), nil
	}
}

// handleAppendOutcome appends an outcome to a memory item.
func handleAppendOutcome(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		memoryID := int64(intArg(req, "memory_id", 0))
		status, _ := req.GetArguments()["status"].(string)
		notes, _ := req.GetArguments()["notes"].(string)
		actorID, _ := req.GetArguments()["actor_id"].(string)

		if memoryID == 0 {
			return mcp.NewToolResultError("memory_id is required"), nil
		}
		if status != "success" && status != "failure" && status != "unknown" {
			return mcp.NewToolResultError("status must be 'success', 'failure', or 'unknown'"), nil
		}
		if actorID == "" {
			actorID = "agent"
		}

		_, err := s.Exec(
			`INSERT INTO memory_outcomes (memory_id, status, notes, actor_id, ts) VALUES (?, ?, ?, ?, ?)`,
			memoryID, status, notes, actorID, time.Now().Format(time.RFC3339),
		)
		if err != nil {
			return mcp.NewToolResultError("Failed to append outcome: " + err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Outcome '%s' recorded for memory %d", status, memoryID)), nil
	}
}

// handleResolveConflict handles memory conflicts via add, merge, invalidate, relate, or suppress actions.
func handleResolveConflict(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		idA := int64(intArg(req, "obs_id_a", 0))
		idB := int64(intArg(req, "obs_id_b", 0))
		action, _ := req.GetArguments()["action"].(string)
		mergedContent, _ := req.GetArguments()["merged_content"].(string)
		relationType, _ := req.GetArguments()["relation_type"].(string)

		if idA == 0 || idB == 0 {
			return mcp.NewToolResultError("obs_id_a and obs_id_b are required"), nil
		}
		if action != "add" && action != "merge" && action != "invalidate" && action != "relate" && action != "suppress" {
			return mcp.NewToolResultError("action must be 'add', 'merge', 'invalidate', 'relate', or 'suppress'"), nil
		}

		switch action {
		case "merge":
			if mergedContent == "" {
				return mcp.NewToolResultError("merged_content is required for merge action"), nil
			}
			// Fetch both memories to build merged title
			memA, err := s.GetMemory(idA)
			if err != nil {
				return mcp.NewToolResultError("Failed to fetch memory A: " + err.Error()), nil
			}
			memB, err := s.GetMemory(idB)
			if err != nil {
				return mcp.NewToolResultError("Failed to fetch memory B: " + err.Error()), nil
			}
			mergedTitle := memA.Title + " + " + memB.Title
			newID, err := s.AddMemory(store.AddMemoryParams{
				ProjectID:  memA.ProjectID,
				Kind:       memA.Kind,
				Title:      util.Truncate(mergedTitle, 200),
				Body:       mergedContent,
				Source:     "conflict-resolution",
				ActorID:    "agent",
				SessionID:  memA.SessionID,
				TrustLevel: "system",
			})
			if err != nil {
				return mcp.NewToolResultError("Failed to create merged memory: " + err.Error()), nil
			}
			// Supersede both source memories
			s.UpdateMemory(idA, store.UpdateMemoryParams{
				Status:       &[]string{store.MemoryStatusSuperseded}[0],
				SupersededBy: &newID,
				ActorID:      "agent",
			})
			s.UpdateMemory(idB, store.UpdateMemoryParams{
				Status:       &[]string{store.MemoryStatusSuperseded}[0],
				SupersededBy: &newID,
				ActorID:      "agent",
			})
			return mcp.NewToolResultText(fmt.Sprintf("Merged memories %d and %d into new memory %d", idA, idB, newID)), nil

		case "add":
			// No structural change: both memories coexist
			return mcp.NewToolResultText(fmt.Sprintf("Conflict marked as false positive (memories %d and %d both coexist — no action taken)", idA, idB)), nil

		case "invalidate":
			// Fetch both memories and mark the older one as expired
			memA, err := s.GetMemory(idA)
			if err != nil {
				return mcp.NewToolResultError("Failed to fetch memory A: " + err.Error()), nil
			}
			memB, err := s.GetMemory(idB)
			if err != nil {
				return mcp.NewToolResultError("Failed to fetch memory B: " + err.Error()), nil
			}
			expiredStatus := "expired"
			if memA.CreatedAt < memB.CreatedAt {
				// memA is older, expire it
				s.UpdateMemory(idA, store.UpdateMemoryParams{Status: &expiredStatus, ActorID: "agent"})
				return mcp.NewToolResultText(fmt.Sprintf("Invalidated older memory %d (created: %s); kept newer memory %d (created: %s)", idA, memA.CreatedAt, idB, memB.CreatedAt)), nil
			} else {
				// memB is older or same, expire it
				s.UpdateMemory(idB, store.UpdateMemoryParams{Status: &expiredStatus, ActorID: "agent"})
				return mcp.NewToolResultText(fmt.Sprintf("Invalidated older memory %d (created: %s); kept newer memory %d (created: %s)", idB, memB.CreatedAt, idA, memA.CreatedAt)), nil
			}

		case "relate":
			// Add relation between the two memories using relation_type
			if relationType == "" {
				return mcp.NewToolResultError("relation_type is required for 'relate' action"), nil
			}
			relA, _ := json.Marshal(map[string]interface{}{"relation_type": relationType, "related_id": idB})
			relB, _ := json.Marshal(map[string]interface{}{"relation_type": relationType, "related_id": idA})
			relAPtr := string(relA)
			relBPtr := string(relB)
			s.UpdateMemory(idA, store.UpdateMemoryParams{RelatedJSON: &relAPtr, ActorID: "agent"})
			s.UpdateMemory(idB, store.UpdateMemoryParams{RelatedJSON: &relBPtr, ActorID: "agent"})
			return mcp.NewToolResultText(fmt.Sprintf("Related memories %d and %d with relation '%s'", idA, idB, relationType)), nil

		case "link":
			// Add each memory ID to the other's related_json
			relA, _ := json.Marshal(map[string][]int64{"relates_to": {idB}})
			relB, _ := json.Marshal(map[string][]int64{"relates_to": {idA}})
			relAPtr := string(relA)
			relBPtr := string(relB)
			s.UpdateMemory(idA, store.UpdateMemoryParams{RelatedJSON: &relAPtr, ActorID: "agent"})
			s.UpdateMemory(idB, store.UpdateMemoryParams{RelatedJSON: &relBPtr, ActorID: "agent"})
			return mcp.NewToolResultText(fmt.Sprintf("Linked memories %d and %d", idA, idB)), nil

		case "suppress":
			// No-op: suppress is recorded as a note but no structural change
			return mcp.NewToolResultText(fmt.Sprintf("Suppressed conflict between memories %d and %d (noted)", idA, idB)), nil
		}

		return mcp.NewToolResultText("Conflict resolved"), nil
	}
}

// handleForget archives a memory with a documented reason.
func handleForget(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(intArg(req, "obs_id", 0))
		reason := req.GetArguments()["reason"].(string)
		replacementObsID, hasReplacement := req.GetArguments()["replacement_obs_id"].(float64)

		if id == 0 {
			return mcp.NewToolResultError("obs_id is required"), nil
		}
		if reason == "" {
			return mcp.NewToolResultError("reason is required"), nil
		}

		// Archive the memory with the documented reason
		if err := s.ForgetMemory(id, reason, "agent"); err != nil {
			return mcp.NewToolResultError("Failed to archive memory: " + err.Error()), nil
		}

		// If a replacement is provided, create supersedes relation
		if hasReplacement {
			replacementID := int64(replacementObsID)
			s.UpdateMemory(id, store.UpdateMemoryParams{
				SupersededBy: &replacementID,
				ActorID:      "agent",
			})
			_ = s.AddRelation(replacementID, id, store.RelationSupersedes)
		}

		msg := fmt.Sprintf("Memory %d archived (reason: %s)", id, reason)
		if hasReplacement {
			msg += fmt.Sprintf("; superseded by memory %d", int64(replacementObsID))
		}
		return mcp.NewToolResultText(msg), nil
	}
}

// handleLink creates a typed relation between two memories.
func handleLink(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromID := int64(intArg(req, "from_obs_id", 0))
		toID := int64(intArg(req, "to_obs_id", 0))
		relation, _ := req.GetArguments()["relation"].(string)

		if fromID == 0 || toID == 0 {
			return mcp.NewToolResultError("from_obs_id and to_obs_id are required"), nil
		}
		validRelations := map[string]bool{"caused": true, "resolves": true, "supersedes": true, "related_to": true, "implements": true, "contradicts": true}
		if !validRelations[relation] {
			return mcp.NewToolResultError("relation must be one of: caused, resolves, supersedes, related_to, implements, contradicts"), nil
		}

		if err := s.AddRelation(fromID, toID, relation); err != nil {
			return mcp.NewToolResultError("Failed to create relation: " + err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Created %s relation: %d → %d", relation, fromID, toID)), nil
	}
}

// handleUnlink removes a typed relation between two memories.
func handleUnlink(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fromID := int64(intArg(req, "from_obs_id", 0))
		toID := int64(intArg(req, "to_obs_id", 0))
		relation, _ := req.GetArguments()["relation"].(string)

		if fromID == 0 || toID == 0 {
			return mcp.NewToolResultError("from_obs_id and to_obs_id are required"), nil
		}
		if relation == "" {
			return mcp.NewToolResultError("relation is required"), nil
		}

		if err := s.RemoveRelation(fromID, toID, relation); err != nil {
			return mcp.NewToolResultError("Failed to remove relation: " + err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Removed %s relation: %d → %d", relation, fromID, toID)), nil
	}
}

// handleRelated traverses relations from a given memory.
func handleRelated(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID := int64(intArg(req, "obs_id", 0))
		relation, _ := req.GetArguments()["relation"].(string)

		if obsID == 0 {
			return mcp.NewToolResultError("obs_id is required"), nil
		}

		items, err := s.GetRelated(obsID, relation)
		if err != nil {
			return mcp.NewToolResultError("Failed to get related memories: " + err.Error()), nil
		}

		if len(items) == 0 {
			return mcp.NewToolResultText("No related memories found."), nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d related memory(ies):\n\n", len(items))
		for i, m := range items {
			preview := util.Truncate(m.Body, 200)
			if len(m.Body) > 200 {
				preview += " [preview]"
			}
			fmt.Fprintf(&b, "[%d] **%s** (%s | %s)\n    %s\n\n", i+1, m.Title, m.Kind, m.Scope, preview)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func handleConsolidationCandidates(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project, _ := req.GetArguments()["project"].(string)
		domain, _ := req.GetArguments()["domain"].(string)

		groups, err := s.GetConsolidationCandidates(project, domain)
		if err != nil {
			return mcp.NewToolResultError("Failed to get consolidation candidates: " + err.Error()), nil
		}

		if len(groups) == 0 {
			return mcp.NewToolResultText("No consolidation candidates found."), nil
		}

		var b strings.Builder
		for i, group := range groups {
			key := group.Candidate.Domain + "/" + group.Candidate.Kind
			if group.Candidate.Domain == "" {
				key = group.Candidate.ProjectID + "/" + group.Candidate.Kind
			}
			fmt.Fprintf(&b, "## %s\n\n", key)
			fmt.Fprintf(&b, "Candidate ID: %d\n", group.Candidate.ID)
			fmt.Fprintf(&b, "Candidate title: **%s**\n", group.Candidate.Title)
			fmt.Fprintf(&b, "Source memories (%d):\n\n", len(group.Sources))
			for _, source := range group.Sources {
				preview := util.Truncate(source.Body, 220)
				fmt.Fprintf(&b, "- ID %d — **%s**\n  Kind: %s\n  %s\n", source.ID, source.Title, source.Kind, preview)
			}
			if i < len(groups)-1 {
				b.WriteString("\n")
			}
		}
		fmt.Fprintf(&b, "\nTotal: %d candidate group(s). Save a semantic memory with mem_save using source='consolidation', then call mem_mark_consolidated with the candidate ID and saved memory ID.", len(groups))
		return mcp.NewToolResultText(b.String()), nil
	}
}

func handleMarkConsolidated(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(intArg(req, "id", 0))
		consolidatedMemoryID := int64(intArg(req, "consolidated_memory_id", 0))

		if id == 0 || consolidatedMemoryID == 0 {
			return mcp.NewToolResultError("id and consolidated_memory_id are required"), nil
		}

		if err := s.MarkConsolidated(id, consolidatedMemoryID); err != nil {
			return mcp.NewToolResultError("Failed to mark consolidated: " + err.Error()), nil
		}

		msg := fmt.Sprintf("Candidate %d archived. Source episodic memories were archived under consolidation memory %d.", id, consolidatedMemoryID)
		return mcp.NewToolResultText(msg), nil
	}
}

func handleFeedback(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID := int64(intArg(req, "obs_id", 0))
		reward := floatArg(req, "reward", 0)
		notes, _ := req.GetArguments()["notes"].(string)
		actorID, _ := req.GetArguments()["actor_id"].(string)
		if obsID == 0 {
			return mcp.NewToolResultError("obs_id is required"), nil
		}
		if err := s.AppendFeedback(obsID, reward, notes, actorID); err != nil {
			return mcp.NewToolResultError("Failed to append feedback: " + err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Feedback recorded for memory %d (reward=%.2f).", obsID, reward)), nil
	}
}

func handleExtractEntities(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obsID := int64(intArg(req, "obs_id", 0))
		projectOverride, _ := req.GetArguments()["project"].(string)
		if obsID == 0 {
			return mcp.NewToolResultError("obs_id is required"), nil
		}
		mem, err := s.GetMemory(obsID)
		if err != nil {
			return mcp.NewToolResultError("Failed to load memory: " + err.Error()), nil
		}
		project := mem.ProjectID
		if projectOverride != "" {
			project = projectOverride
		}
		names := store.ExtractEntitiesHeuristic(mem.Title + "\n" + mem.Body)
		if len(names) == 0 {
			return mcp.NewToolResultText("No entities extracted."), nil
		}
		count, err := s.AttachExtractedEntities(obsID, project, names)
		if err != nil {
			return mcp.NewToolResultError("Failed to link entities: " + err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Extracted and linked %d entities for memory %d.", count, obsID)), nil
	}
}

func handleGraphContext(s *store.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		entity, _ := req.GetArguments()["entity"].(string)
		project, _ := req.GetArguments()["project"].(string)
		limit := intArg(req, "limit", 10)
		if strings.TrimSpace(entity) == "" {
			return mcp.NewToolResultError("entity is required"), nil
		}
		items, err := s.GraphContext(project, entity, limit)
		if err != nil {
			return mcp.NewToolResultError("Failed to build graph context: " + err.Error()), nil
		}
		if len(items) == 0 {
			return mcp.NewToolResultText("No graph-linked memories found for that entity."), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Graph context for entity %q (%d items):\n\n", entity, len(items))
		for i, m := range items {
			preview := util.Truncate(m.Body, 200)
			fmt.Fprintf(&b, "[%d] #%d **%s** (%s | %s)\n    %s\n\n", i+1, m.ID, m.Title, m.Kind, m.Scope, preview)
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// defaultSessionID returns a project-scoped default session ID.
// If project is non-empty: "manual-save-{project}"
// If project is empty: "manual-save"
func defaultSessionID(project string) string {
	if project == "" {
		return "manual-save"
	}
	return "manual-save-" + project
}

func intArg(req mcp.CallToolRequest, key string, defaultVal int) int {
	v, ok := req.GetArguments()[key].(float64)
	if !ok {
		return defaultVal
	}
	return int(v)
}

func floatArg(req mcp.CallToolRequest, key string, defaultVal float64) float64 {
	v, ok := req.GetArguments()[key].(float64)
	if !ok {
		return defaultVal
	}
	return v
}

func boolArg(req mcp.CallToolRequest, key string, defaultVal bool) bool {
	v, ok := req.GetArguments()[key].(bool)
	if !ok {
		return defaultVal
	}
	return v
}

// estimateTokens returns the estimated token count for a string.
func estimateTokens(text string) int {
	return token.Count(text)
}
