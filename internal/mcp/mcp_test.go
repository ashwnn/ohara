package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/store"
	"github.com/ashwnn/ohara/internal/util"
	mcppkg "github.com/mark3labs/mcp-go/mcp"
)

func newMCPTestStore(t *testing.T) *store.Store {
	t.Helper()
	cfg, err := store.DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.DataDir = t.TempDir()

	s, err := store.New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

func callResultText(t *testing.T, res *mcppkg.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("expected non-empty tool result")
	}
	text, ok := mcppkg.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("expected text content")
	}
	return text.Text
}

func TestNewServerRegistersTools(t *testing.T) {
	s := newMCPTestStore(t)
	srv := NewServer(s)
	if srv == nil {
		t.Fatalf("expected MCP server instance")
	}
}

func TestHandleSuggestTopicKeyReturnsFamilyBasedKey(t *testing.T) {
	h := handleSuggestTopicKey()
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"type":  "architecture",
		"title": "Auth model",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "Suggested topic_key: architecture/auth-model") {
		t.Fatalf("unexpected suggestion output: %q", text)
	}
}

func TestHandleSuggestTopicKeyRequiresInput(t *testing.T) {
	h := handleSuggestTopicKey()
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error when input is empty")
	}
}

func TestHandleSaveSuggestsTopicKeyWhenMissing(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Auth architecture",
		"content": "Define boundaries for auth middleware",
		"type":    "architecture",
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected save error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "Suggested topic_key: decision/auth-architecture") {
		t.Fatalf("expected suggestion in save response, got %q", text)
	}
}

func TestHandleSaveDoesNotSuggestWhenTopicKeyProvided(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":     "Auth architecture",
		"content":   "Define boundaries for auth middleware",
		"type":      "architecture",
		"project":   "ohara",
		"topic_key": "architecture/auth-model",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected save error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if strings.Contains(text, "Suggested topic_key:") {
		t.Fatalf("did not expect suggestion when topic_key provided, got %q", text)
	}
}

func TestHandleCapturePassiveExtractsAndSaves(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleCapturePassive(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "## Key Learnings:\n\n1. bcrypt cost=12 is the right balance for our server\n2. JWT refresh tokens need atomic rotation to prevent races\n",
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "extracted=2") {
		t.Fatalf("expected extracted=2 in response, got %q", text)
	}
	if !strings.Contains(text, "saved=2") {
		t.Fatalf("expected saved=2 in response, got %q", text)
	}
}

func TestHandleCapturePassiveRequiresContent(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleCapturePassive(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error when content is missing")
	}
}

func TestHandleCapturePassiveWithNoLearningSection(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleCapturePassive(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "plain text without learning headers",
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "extracted=0") || !strings.Contains(text, "saved=0") {
		t.Fatalf("expected zero extraction/save counters, got %q", text)
	}
}

func TestHandleCapturePassiveDefaultsSourceAndSession(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleCapturePassive(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "## Key Learnings:\n\n1. This learning is long enough to be persisted with default source",
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", callResultText(t, res))
	}

	mems, err := s.GetMemories("ohara", "project", "", store.MemoryStatusActive, 5)
	if err != nil {
		t.Fatalf("get memories: %v", err)
	}
	if len(mems) == 0 {
		t.Fatalf("expected at least one memory")
	}
	if mems[0].Source == "" || mems[0].Source != "passive" {
		t.Fatalf("expected default source passive, got %+v", mems[0].Source)
	}
}

func TestHandleCapturePassiveReturnsToolErrorOnStoreFailure(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleCapturePassive(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "## Key Learnings:\n\n1. This learning is long enough to trigger insert and fail",
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error when store is closed")
	}
}

func TestHelperArgsAndTruncate(t *testing.T) {
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"limit": 7.0,
		"flag":  true,
	}}}

	if got := intArg(req, "limit", 10); got != 7 {
		t.Fatalf("expected intArg=7, got %d", got)
	}
	if got := intArg(req, "missing", 10); got != 10 {
		t.Fatalf("expected default intArg=10, got %d", got)
	}
	if got := boolArg(req, "flag", false); !got {
		t.Fatalf("expected boolArg true")
	}
	if got := boolArg(req, "missing", true); !got {
		t.Fatalf("expected default boolArg=true")
	}

	if got := util.Truncate("short", 10); got != "short" {
		t.Fatalf("unexpected truncate for short input: %q", got)
	}
	if got := util.Truncate("this is long", 4); got != "this..." {
		t.Fatalf("unexpected truncate for long input: %q", got)
	}
	// Multibyte UTF-8 safety
	if got := util.Truncate("Decisión de arquitectura", 8); got != "Decisión..." {
		t.Fatalf("truncate spanish accents = %q, want %q", got, "Decisión...")
	}
	if got := util.Truncate("🐛🔧🚀✨🎉💡", 3); got != "🐛🔧🚀..." {
		t.Fatalf("truncate emoji = %q, want %q", got, "🐛🔧🚀...")
	}
	if got := util.Truncate("café☕latte", 5); got != "café☕..." {
		t.Fatalf("truncate mixed = %q, want %q", got, "café☕...")
	}
}

func TestHandleSearchAndCRUDHandlers(t *testing.T) {
	s := newMCPTestStore(t)
	if err := s.CreateSession("s-mcp", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	memID, err := s.AddMemory(store.AddMemoryParams{
		SessionID: "s-mcp",
		Kind:      "bugfix",
		Title:     "Fix panic",
		Body:      "Fix panic in parser branch when args are missing",
		ProjectID: "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	search := handleSearch(s, MCPConfig{}, NewSessionActivity(10*time.Minute))
	searchReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"query":   "panic",
		"project": "ohara",
		"scope":   "project",
		"limit":   5.0,
	}}}
	searchRes, err := search(context.Background(), searchReq)
	if err != nil {
		t.Fatalf("search handler error: %v", err)
	}
	if searchRes.IsError {
		t.Fatalf("unexpected search error: %s", callResultText(t, searchRes))
	}
	searchText := callResultText(t, searchRes)
	// The memory was added via AddMemory, so search finds it via SearchMemories
	if !strings.Contains(searchText, "Found 1 memory item(s)") {
		t.Fatalf("expected non-empty search result, got: %q", searchText)
	}

	update := handleUpdate(s)
	updateReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"id":    float64(memID),
		"title": "Fix parser panic",
	}}}
	updateRes, err := update(context.Background(), updateReq)
	if err != nil {
		t.Fatalf("update handler error: %v", err)
	}
	if updateRes.IsError {
		t.Fatalf("unexpected update error: %s", callResultText(t, updateRes))
	}

	deleteHandler := handleDelete(s)
	delReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"id":          float64(memID),
		"hard_delete": true,
	}}}
	delRes, err := deleteHandler(context.Background(), delReq)
	if err != nil {
		t.Fatalf("delete handler error: %v", err)
	}
	if delRes.IsError {
		t.Fatalf("unexpected delete error: %s", callResultText(t, delRes))
	}
	if !strings.Contains(callResultText(t, delRes), "deleted") {
		t.Fatalf("expected delete message")
	}
}

func TestHandlePromptContextStatsTimelineAndSessionHandlers(t *testing.T) {
	s := newMCPTestStore(t)
	if err := s.CreateSession("s-flow", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Seed memory item for handleContext (uses BuildPack/memory_items)
	_, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      "decision",
		Title:     "Auth decision",
		Body:      "Keep auth in middleware",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	savePrompt := handleSavePrompt(s, MCPConfig{})
	savePromptReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "how do we fix auth race conditions?",
		"project": "ohara",
	}}}
	savePromptRes, err := savePrompt(context.Background(), savePromptReq)
	if err != nil {
		t.Fatalf("save prompt handler error: %v", err)
	}
	if savePromptRes.IsError {
		t.Fatalf("unexpected save prompt error: %s", callResultText(t, savePromptRes))
	}

	contextHandler := handleContext(s, MCPConfig{}, NewSessionActivity(10*time.Minute))
	contextReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"project": "ohara",
		"scope":   "project",
	}}}
	contextRes, err := contextHandler(context.Background(), contextReq)
	if err != nil {
		t.Fatalf("context handler error: %v", err)
	}
	if contextRes.IsError {
		t.Fatalf("unexpected context error: %s", callResultText(t, contextRes))
	}
	if !strings.Contains(callResultText(t, contextRes), "Memory stats") {
		t.Fatalf("expected context output with memory stats")
	}

	statsHandler := handleStats(s)
	statsRes, err := statsHandler(context.Background(), mcppkg.CallToolRequest{})
	if err != nil {
		t.Fatalf("stats handler error: %v", err)
	}
	if statsRes.IsError {
		t.Fatalf("unexpected stats error: %s", callResultText(t, statsRes))
	}

	recent, err := s.GetMemories("ohara", "project", "", store.MemoryStatusActive, 1)
	if err != nil || len(recent) == 0 {
		t.Fatalf("get memories for timeline: %v len=%d", err, len(recent))
	}

	focusID := recent[0].ID
	timelineHandler := handleTimeline(s)
	timelineReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"memory_id": float64(focusID),
		"before":    2.0,
		"after":     2.0,
	}}}
	timelineRes, err := timelineHandler(context.Background(), timelineReq)
	if err != nil {
		t.Fatalf("timeline handler error: %v", err)
	}
	if timelineRes.IsError {
		t.Fatalf("unexpected timeline error: %s", callResultText(t, timelineRes))
	}

	sessionSummary := handleSessionSummary(s, MCPConfig{}, NewSessionActivity(10*time.Minute))
	summaryReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"project": "ohara",
		"content": "## Goal\nImprove tests",
	}}}
	summaryRes, err := sessionSummary(context.Background(), summaryReq)
	if err != nil {
		t.Fatalf("session summary handler error: %v", err)
	}
	if summaryRes.IsError {
		t.Fatalf("unexpected session summary error: %s", callResultText(t, summaryRes))
	}

	sessionStart := handleSessionStart(s, MCPConfig{}, NewSessionActivity(10*time.Minute))
	startReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"id":        "s-new",
		"project":   "ohara",
		"directory": "/tmp/ohara",
	}}}
	startRes, err := sessionStart(context.Background(), startReq)
	if err != nil {
		t.Fatalf("session start handler error: %v", err)
	}
	if startRes.IsError {
		t.Fatalf("unexpected session start error: %s", callResultText(t, startRes))
	}

	sessionEnd := handleSessionEnd(s, MCPConfig{}, NewSessionActivity(10*time.Minute))
	endReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"id":      "s-new",
		"summary": "done",
	}}}
	endRes, err := sessionEnd(context.Background(), endReq)
	if err != nil {
		t.Fatalf("session end handler error: %v", err)
	}
	if endRes.IsError {
		t.Fatalf("unexpected session end error: %s", callResultText(t, endRes))
	}
}

func TestMCPHandlersErrorBranches(t *testing.T) {
	s := newMCPTestStore(t)

	search := handleSearch(s, MCPConfig{}, NewSessionActivity(10*time.Minute))
	noResultsReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"query": "definitely-no-hit"}}}
	noResultsRes, err := search(context.Background(), noResultsReq)
	if err != nil {
		t.Fatalf("search handler error: %v", err)
	}
	if noResultsRes.IsError {
		t.Fatalf("expected non-error no-results response")
	}
	if !strings.Contains(callResultText(t, noResultsRes), "No memories found") {
		t.Fatalf("expected no memories response")
	}

	update := handleUpdate(s)
	missingIDRes, err := update(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{}}})
	if err != nil {
		t.Fatalf("update missing id error: %v", err)
	}
	if !missingIDRes.IsError {
		t.Fatalf("expected update missing id to return tool error")
	}

	noFieldsReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"id": 1.0}}}
	noFieldsRes, err := update(context.Background(), noFieldsReq)
	if err != nil {
		t.Fatalf("update no fields error: %v", err)
	}
	if !noFieldsRes.IsError {
		t.Fatalf("expected update no fields to return tool error")
	}

	deleteHandler := handleDelete(s)
	delMissingIDRes, err := deleteHandler(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{}}})
	if err != nil {
		t.Fatalf("delete missing id error: %v", err)
	}
	if !delMissingIDRes.IsError {
		t.Fatalf("expected delete missing id to return tool error")
	}

	timeline := handleTimeline(s)
	timelineMissingIDRes, err := timeline(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{}}})
	if err != nil {
		t.Fatalf("timeline missing id error: %v", err)
	}
	if !timelineMissingIDRes.IsError {
		t.Fatalf("expected timeline missing id to return tool error")
	}
}

func TestMCPHandlersReturnErrorsWhenStoreClosed(t *testing.T) {
	s := newMCPTestStore(t)
	if err := s.CreateSession("s-closed", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddMemory(store.AddMemoryParams{
		SessionID: "s-closed",
		Kind:      "decision",
		Title:     "Title",
		Body:      "Content",
		ProjectID: "ohara",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	searchRes, err := handleSearch(s, MCPConfig{}, NewSessionActivity(10*time.Minute))(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"query": "title"}}})
	if err != nil {
		t.Fatalf("closed store search call: %v", err)
	}
	if !searchRes.IsError {
		t.Fatalf("expected search to return tool error when store is closed")
	}

	updateRes, err := handleUpdate(s)(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"id": 1.0, "title": "new"}}})
	if err != nil {
		t.Fatalf("closed store update call: %v", err)
	}
	if !updateRes.IsError {
		t.Fatalf("expected update to return tool error when store is closed")
	}

	deleteRes, err := handleDelete(s)(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"id": 1.0}}})
	if err != nil {
		t.Fatalf("closed store delete call: %v", err)
	}
	if !deleteRes.IsError {
		t.Fatalf("expected delete to return tool error when store is closed")
	}

	promptRes, err := handleSavePrompt(s, MCPConfig{})(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"content": "prompt", "project": "ohara"}}})
	if err != nil {
		t.Fatalf("closed store save prompt call: %v", err)
	}
	if !promptRes.IsError {
		t.Fatalf("expected save prompt to return tool error when store is closed")
	}

	contextRes, err := handleContext(s, MCPConfig{}, NewSessionActivity(10*time.Minute))(context.Background(), mcppkg.CallToolRequest{})
	if err != nil {
		t.Fatalf("closed store context call: %v", err)
	}
	if !contextRes.IsError {
		t.Fatalf("expected context to return tool error when store is closed")
	}

	statsRes, err := handleStats(s)(context.Background(), mcppkg.CallToolRequest{})
	if err != nil {
		t.Fatalf("closed store stats call: %v", err)
	}
	if statsRes.IsError {
		t.Fatalf("expected stats fallback result even when store is closed")
	}

	// mem_timeline on closed store should return an error
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	timelineRes, err := handleTimeline(s)(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"memory_id": 1.0}}})
	if err != nil {
		t.Fatalf("closed store timeline call: %v", err)
	}
	if !timelineRes.IsError {
		t.Fatalf("expected timeline to return tool error when store is closed")
	}

	sessionSummaryRes, err := handleSessionSummary(s, MCPConfig{}, NewSessionActivity(10*time.Minute))(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"project": "ohara", "content": "summary"}}})
	if err != nil {
		t.Fatalf("closed store session summary call: %v", err)
	}
	if !sessionSummaryRes.IsError {
		t.Fatalf("expected session summary to return tool error when store is closed")
	}

	sessionStartRes, err := handleSessionStart(s, MCPConfig{}, NewSessionActivity(10*time.Minute))(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"id": "s1", "project": "ohara"}}})
	if err != nil {
		t.Fatalf("closed store session start call: %v", err)
	}
	if !sessionStartRes.IsError {
		t.Fatalf("expected session start to return tool error when store is closed")
	}

	sessionEndRes, err := handleSessionEnd(s, MCPConfig{}, NewSessionActivity(10*time.Minute))(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"id": "s1"}}})
	if err != nil {
		t.Fatalf("closed store session end call: %v", err)
	}
	if !sessionEndRes.IsError {
		t.Fatalf("expected session end to return tool error when store is closed")
	}
}

func TestMCPAdditionalCoverageBranches(t *testing.T) {
	s := newMCPTestStore(t)

	contextRes, err := handleContext(s, MCPConfig{}, NewSessionActivity(10*time.Minute))(context.Background(), mcppkg.CallToolRequest{})
	if err != nil {
		t.Fatalf("context empty store: %v", err)
	}
	if contextRes.IsError {
		t.Fatalf("expected non-error context for empty store")
	}
	if !strings.Contains(callResultText(t, contextRes), "No previous session memories found") {
		t.Fatalf("expected empty context message")
	}

	statsRes, err := handleStats(s)(context.Background(), mcppkg.CallToolRequest{})
	if err != nil {
		t.Fatalf("stats empty store: %v", err)
	}
	if statsRes.IsError {
		t.Fatalf("expected non-error stats for empty store")
	}
	if !strings.Contains(callResultText(t, statsRes), "Projects: none yet") {
		t.Fatalf("expected none yet projects in stats output")
	}

	if err := s.CreateSession("s-extra", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	firstID, err := s.AddMemory(store.AddMemoryParams{SessionID: "s-extra", Kind: store.MemoryKindDiscovery, Title: "first", Body: "first content", ProjectID: "ohara"})
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	_, err = s.AddMemory(store.AddMemoryParams{SessionID: "s-extra", Kind: store.MemoryKindDiscovery, Title: "second", Body: "second content", ProjectID: "ohara"})
	if err != nil {
		t.Fatalf("add second: %v", err)
	}

	timelineReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{"memory_id": float64(firstID), "before": 1.0, "after": 2.0}}}
	timelineRes, err := handleTimeline(s)(context.Background(), timelineReq)
	if err != nil {
		t.Fatalf("timeline with header branches: %v", err)
	}
	if timelineRes.IsError {
		t.Fatalf("expected non-error timeline with data")
	}
	text := callResultText(t, timelineRes)
	if !strings.Contains(text, "Memory #") || !strings.Contains(text, "After") {
		t.Fatalf("expected timeline session/after sections, got %q", text)
	}

	save := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))
	saveReq := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Default values",
		"content": "Ensure defaults for type and session are used",
		"project": "ohara",
	}}}
	saveRes, err := save(context.Background(), saveReq)
	if err != nil {
		t.Fatalf("save defaults: %v", err)
	}
	if saveRes.IsError {
		t.Fatalf("expected save defaults to succeed: %s", callResultText(t, saveRes))
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	saveClosedRes, err := save(context.Background(), saveReq)
	if err != nil {
		t.Fatalf("save closed store call: %v", err)
	}
	if !saveClosedRes.IsError {
		t.Fatalf("expected save to fail when store is closed")
	}
}

func TestHandleSuggestTopicKeyReturnsErrorWhenSuggestionEmpty(t *testing.T) {
	prev := suggestTopicKey
	suggestTopicKey = func(typ, title, content string) string {
		return ""
	}
	t.Cleanup(func() {
		suggestTopicKey = prev
	})

	h := handleSuggestTopicKey()
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title": "valid title",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error when suggestion is empty")
	}
}

func TestHandleUpdateAcceptsAllOptionalFields(t *testing.T) {
	s := newMCPTestStore(t)
	if err := s.CreateSession("s-all-fields", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	id, err := s.AddMemory(store.AddMemoryParams{
		SessionID: "s-all-fields",
		Kind:      "decision",
		Title:     "Original",
		Body:      "Original content",
		ProjectID: "ohara",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	res, err := handleUpdate(s)(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"id":        float64(id),
		"title":     "Updated",
		"content":   "Updated content",
		"type":      "architecture",
		"project":   "ohara",
		"scope":     "personal",
		"topic_key": "architecture/auth-model",
	}}})
	if err != nil {
		t.Fatalf("update handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected update error: %s", callResultText(t, res))
	}
}

func TestHandleContextWithSessionOnlyUsesNoneProjects(t *testing.T) {
	s := newMCPTestStore(t)
	if err := s.CreateSession("s-context-none", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Seed memory item for handleContext (uses BuildPack/memory_items)
	_, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      "decision",
		Title:     "Test decision",
		Body:      "Test content for context",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

	res, err := handleContext(s, MCPConfig{}, NewSessionActivity(10*time.Minute))(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"project": "ohara",
	}}})
	if err != nil {
		t.Fatalf("context handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected context error: %s", callResultText(t, res))
	}
	if !strings.Contains(callResultText(t, res), "projects: ohara") {
		t.Fatalf("expected context output with projects: ohara")
	}
}

func TestHandleStatsReturnsErrorWhenLoaderFails(t *testing.T) {
	prev := loadMCPStatsCombined
	loadMCPStatsCombined = func(s *store.Store) (*store.Stats, *store.PackStats, error) {
		return nil, nil, errors.New("stats unavailable")
	}
	t.Cleanup(func() {
		loadMCPStatsCombined = prev
	})

	s := newMCPTestStore(t)
	res, err := handleStats(s)(context.Background(), mcppkg.CallToolRequest{})
	if err != nil {
		t.Fatalf("stats handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error when stats loader fails")
	}
}

func TestHandleTimelineBeforeSectionAndSummaryBranches(t *testing.T) {
	s := newMCPTestStore(t)
	if err := s.CreateSession("s-timeline", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, err := s.AddMemory(store.AddMemoryParams{SessionID: "s-timeline", Kind: store.MemoryKindDiscovery, Title: "first", Body: "first", ProjectID: "ohara"})
	if err != nil {
		t.Fatalf("add first memory: %v", err)
	}
	focusID, err := s.AddMemory(store.AddMemoryParams{SessionID: "s-timeline", Kind: store.MemoryKindDiscovery, Title: "second", Body: "second", ProjectID: "ohara"})
	if err != nil {
		t.Fatalf("add second memory: %v", err)
	}
	_, err = s.AddMemory(store.AddMemoryParams{SessionID: "s-timeline", Kind: store.MemoryKindDiscovery, Title: "third", Body: "third", ProjectID: "ohara"})
	if err != nil {
		t.Fatalf("add third memory: %v", err)
	}
	if err := s.EndSession("s-timeline", "timeline summary"); err != nil {
		t.Fatalf("end session: %v", err)
	}

	res, err := handleTimeline(s)(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"memory_id": float64(focusID),
		"before":    2.0,
		"after":     1.0,
	}}})
	if err != nil {
		t.Fatalf("timeline handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected timeline error: %s", callResultText(t, res))
	}
	text := callResultText(t, res)
	if !strings.Contains(text, "Memory #") {
		t.Fatalf("expected timeline output with memory, got %q", text)
	}
}

// ─── Tool Profile Tests ─────────────────────────────────────────────────────

func TestResolveToolsEmpty(t *testing.T) {
	result := ResolveTools("")
	if result != nil {
		t.Fatalf("expected nil for empty input, got %v", result)
	}
}

func TestResolveToolsAll(t *testing.T) {
	result := ResolveTools("all")
	if result != nil {
		t.Fatalf("expected nil for 'all', got %v", result)
	}
}

func TestResolveToolsAgentProfile(t *testing.T) {
	result := ResolveTools("agent")
	if result == nil {
		t.Fatal("expected non-nil allowlist for 'agent'")
	}

	expectedTools := []string{
		"mem_save", "mem_search", "mem_context", "mem_session_summary",
		"mem_session_start", "mem_session_end",
		"mem_suggest_topic_key", "mem_capture_passive", "mem_save_prompt",
		"mem_update", "mem_pack", "mem_prime", "mem_mark_used",
		"mem_append_outcome", "mem_resolve_conflict", "mem_forget", "mem_link",
		"mem_unlink", "mem_related", "mem_consolidate_candidates", "mem_mark_consolidated",
		"mem_search_rerank", "mem_feedback", "mem_graph_context", "mem_extract_entities",
	}
	for _, tool := range expectedTools {
		if !result[tool] {
			t.Errorf("agent profile missing tool: %s", tool)
		}
	}

	// Admin-only tools should NOT be in agent profile
	adminOnly := []string{"mem_delete", "mem_stats", "mem_timeline"}
	for _, tool := range adminOnly {
		if result[tool] {
			t.Errorf("agent profile should NOT contain admin tool: %s", tool)
		}
	}

	if len(result) != 25 {
		t.Errorf("agent profile has %d tools, expected 25", len(result))
	}
}

func TestResolveToolsAdminProfile(t *testing.T) {
	result := ResolveTools("admin")
	if result == nil {
		t.Fatal("expected non-nil allowlist for 'admin'")
	}

	expectedTools := []string{"mem_delete", "mem_stats", "mem_timeline", "mem_merge_projects", "mem_list_domains"}
	for _, tool := range expectedTools {
		if !result[tool] {
			t.Errorf("admin profile missing tool: %s", tool)
		}
	}

	if len(result) != len(expectedTools) {
		t.Errorf("admin profile has %d tools, expected %d", len(result), len(expectedTools))
	}
}

func TestResolveToolsCombinedProfiles(t *testing.T) {
	result := ResolveTools("agent,admin")
	if result == nil {
		t.Fatal("expected non-nil allowlist for combined profiles")
	}

	// Should have all 21 tools
	allTools := []string{
		"mem_save", "mem_search", "mem_context", "mem_session_summary",
		"mem_session_start", "mem_session_end",
		"mem_suggest_topic_key", "mem_capture_passive", "mem_save_prompt",
		"mem_update", "mem_delete", "mem_stats", "mem_timeline", "mem_merge_projects",
		"mem_pack", "mem_prime", "mem_mark_used", "mem_append_outcome",
		"mem_resolve_conflict", "mem_list_domains", "mem_forget", "mem_link",
		"mem_unlink", "mem_related", "mem_consolidate_candidates", "mem_mark_consolidated",
		"mem_search_rerank", "mem_feedback", "mem_graph_context", "mem_extract_entities",
	}
	for _, tool := range allTools {
		if !result[tool] {
			t.Errorf("combined profile missing tool: %s", tool)
		}
	}
}

func TestResolveToolsIndividualNames(t *testing.T) {
	result := ResolveTools("mem_save,mem_search")
	if result == nil {
		t.Fatal("expected non-nil allowlist")
	}

	if !result["mem_save"] || !result["mem_search"] {
		t.Fatalf("expected mem_save and mem_search, got %v", result)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 tools, got %d", len(result))
	}
}

func TestResolveToolsMixedProfileAndNames(t *testing.T) {
	result := ResolveTools("admin,mem_save")
	if result == nil {
		t.Fatal("expected non-nil allowlist")
	}

	// Should have admin tools + mem_save
	if !result["mem_save"] {
		t.Error("missing mem_save")
	}
	if !result["mem_stats"] {
		t.Error("missing mem_stats from admin profile")
	}
	if !result["mem_timeline"] {
		t.Error("missing mem_timeline from admin profile")
	}
}

func TestResolveToolsAllInMixed(t *testing.T) {
	result := ResolveTools("agent,all")
	if result != nil {
		t.Fatalf("expected nil when 'all' is in the mix, got %v", result)
	}
}

func TestResolveToolsWhitespace(t *testing.T) {
	result := ResolveTools("  agent  ")
	if result == nil {
		t.Fatal("expected non-nil for agent with whitespace")
	}
	if !result["mem_save"] {
		t.Error("agent profile should include mem_save")
	}
}

func TestResolveToolsCommaWhitespace(t *testing.T) {
	result := ResolveTools("mem_save , mem_search")
	if result == nil {
		t.Fatal("expected non-nil allowlist")
	}
	if !result["mem_save"] || !result["mem_search"] {
		t.Fatalf("expected both tools, got %v", result)
	}
}

func TestResolveToolsEmptyTokenBetweenCommas(t *testing.T) {
	result := ResolveTools("mem_save,,mem_search")
	if result == nil {
		t.Fatal("expected non-nil allowlist")
	}
	if !result["mem_save"] || !result["mem_search"] {
		t.Fatalf("expected mem_save and mem_search in result, got %v", result)
	}
}

func TestResolveToolsAllAfterRealTool(t *testing.T) {
	result := ResolveTools("mem_save,all")
	if result != nil {
		t.Fatalf("expected nil when 'all' appears anywhere in list, got %v", result)
	}
}

func TestResolveToolsOnlyCommas(t *testing.T) {
	result := ResolveTools(",,,")
	if result != nil {
		t.Fatalf("expected nil when input is only commas (empty tokens), got %v", result)
	}
}

func TestShouldRegisterNilAllowlist(t *testing.T) {
	if !shouldRegister("anything", nil) {
		t.Error("nil allowlist should allow everything")
	}
}

func TestShouldRegisterWithAllowlist(t *testing.T) {
	allowlist := map[string]bool{"mem_save": true, "mem_search": true}

	if !shouldRegister("mem_save", allowlist) {
		t.Error("mem_save should be allowed")
	}
	if shouldRegister("mem_delete", allowlist) {
		t.Error("mem_delete should NOT be allowed")
	}
}

func TestNewServerWithToolsAgentProfile(t *testing.T) {
	s := newMCPTestStore(t)
	allowlist := ResolveTools("agent")

	srv := NewServerWithTools(s, allowlist)
	if srv == nil {
		t.Fatal("expected MCP server instance")
	}

	tools := srv.ListTools()

	// Agent tools should be present (15 tools)
	agentTools := []string{
		"mem_save", "mem_search", "mem_context", "mem_session_summary",
		"mem_session_start", "mem_session_end",
		"mem_suggest_topic_key", "mem_capture_passive", "mem_save_prompt",
		"mem_update", "mem_pack", "mem_prime", "mem_mark_used",
		"mem_append_outcome", "mem_resolve_conflict",
	}
	for _, name := range agentTools {
		if tools[name] == nil {
			t.Errorf("agent profile: expected tool %q to be registered", name)
		}
	}

	// Admin-only tools should NOT be present
	adminTools := []string{"mem_delete", "mem_stats", "mem_timeline"}
	for _, name := range adminTools {
		if tools[name] != nil {
			t.Errorf("agent profile: tool %q should NOT be registered", name)
		}
	}
}

func TestNewServerWithToolsAdminProfile(t *testing.T) {
	s := newMCPTestStore(t)
	allowlist := ResolveTools("admin")

	srv := NewServerWithTools(s, allowlist)
	if srv == nil {
		t.Fatal("expected MCP server instance")
	}

	tools := srv.ListTools()

	// Admin tools should be present (5 tools)
	adminTools := []string{"mem_delete", "mem_stats", "mem_timeline", "mem_merge_projects", "mem_list_domains"}
	for _, name := range adminTools {
		if tools[name] == nil {
			t.Errorf("admin profile: expected tool %q to be registered", name)
		}
	}

	// Agent-only tools should NOT be present
	agentOnlyTools := []string{"mem_save", "mem_search", "mem_context", "mem_update"}
	for _, name := range agentOnlyTools {
		if tools[name] != nil {
			t.Errorf("admin profile: tool %q should NOT be registered", name)
		}
	}
}

func TestNewServerWithToolsNilRegistersAll(t *testing.T) {
	s := newMCPTestStore(t)

	srv := NewServerWithTools(s, nil)
	if srv == nil {
		t.Fatal("expected MCP server instance")
	}

	tools := srv.ListTools()

	allTools := []string{
		"mem_save", "mem_search", "mem_context", "mem_session_summary",
		"mem_session_start", "mem_session_end",
		"mem_suggest_topic_key", "mem_capture_passive", "mem_save_prompt",
		"mem_update", "mem_delete", "mem_stats", "mem_timeline", "mem_merge_projects",
		"mem_pack", "mem_prime", "mem_mark_used", "mem_append_outcome",
		"mem_resolve_conflict", "mem_list_domains",
	}

	for _, name := range allTools {
		if tools[name] == nil {
			t.Errorf("nil allowlist: expected tool %q to be registered", name)
		}
	}

	if len(tools) != 30 {
		t.Errorf("expected 30 tools with nil allowlist, got %d", len(tools))
	}
}

func TestNewServerWithToolsIndividualSelection(t *testing.T) {
	s := newMCPTestStore(t)
	allowlist := ResolveTools("mem_save,mem_search")

	srv := NewServerWithTools(s, allowlist)
	tools := srv.ListTools()

	if tools["mem_save"] == nil {
		t.Error("expected mem_save to be registered")
	}
	if tools["mem_search"] == nil {
		t.Error("expected mem_search to be registered")
	}
	if len(tools) != 2 {
		t.Errorf("expected exactly 2 tools, got %d", len(tools))
	}
}

func TestNewServerBackwardsCompatible(t *testing.T) {
	s := newMCPTestStore(t)

	// NewServer (no tools filter) should register all tools
	srv := NewServer(s)
	tools := srv.ListTools()

	// 25 agent + 5 admin = 30 total
	if len(tools) != 30 {
		t.Errorf("NewServer should register all 30 tools, got %d", len(tools))
	}
}

func TestProfileConsistency(t *testing.T) {
	// Verify that agent + admin = all 30 tools
	combined := make(map[string]bool)
	for tool := range ProfileAgent {
		combined[tool] = true
	}
	for tool := range ProfileAdmin {
		combined[tool] = true
	}

	if len(combined) != 30 {
		t.Errorf("agent + admin should cover all 30 tools, got %d", len(combined))
	}

	// Verify no overlap between profiles
	for tool := range ProfileAgent {
		if ProfileAdmin[tool] {
			t.Errorf("tool %q appears in both agent and admin profiles", tool)
		}
	}
}

// ─── Server Instructions ─────────────────────────────────────────────────────

func TestServerInstructionsConstantIsNonEmpty(t *testing.T) {
	if serverInstructions == "" {
		t.Fatal("serverInstructions should not be empty — it drives Tool Search discovery")
	}
	// Must mention key tool names so Tool Search can index them
	for _, keyword := range []string{"mem_save", "mem_search", "mem_context", "mem_session_summary"} {
		if !strings.Contains(serverInstructions, keyword) {
			t.Errorf("serverInstructions should mention %q for Tool Search indexing", keyword)
		}
	}
}

// ─── Tool Annotations ────────────────────────────────────────────────────────

func TestCoreToolsAreNotDeferred(t *testing.T) {
	s := newMCPTestStore(t)
	srv := NewServer(s)
	tools := srv.ListTools()

	coreTools := []string{
		"mem_save", "mem_search", "mem_context", "mem_session_summary",
		"mem_save_prompt",
	}
	for _, name := range coreTools {
		tool := tools[name]
		if tool == nil {
			t.Errorf("core tool %q should be registered", name)
			continue
		}
		if tool.Tool.DeferLoading {
			t.Errorf("core tool %q should NOT have DeferLoading=true — it must always be in context", name)
		}
	}
}

func TestNonCoreToolsAreDeferred(t *testing.T) {
	s := newMCPTestStore(t)
	srv := NewServer(s)
	tools := srv.ListTools()

	deferredTools := []string{
		"mem_update", "mem_suggest_topic_key",
		"mem_session_start", "mem_session_end",
		"mem_stats", "mem_delete", "mem_timeline",
		"mem_capture_passive", "mem_merge_projects",
	}
	for _, name := range deferredTools {
		tool := tools[name]
		if tool == nil {
			t.Errorf("deferred tool %q should be registered", name)
			continue
		}
		if !tool.Tool.DeferLoading {
			t.Errorf("non-core tool %q should have DeferLoading=true", name)
		}
	}
}

func TestAllToolsHaveAnnotations(t *testing.T) {
	s := newMCPTestStore(t)
	srv := NewServer(s)
	tools := srv.ListTools()

	for name, tool := range tools {
		ann := tool.Tool.Annotations
		if ann.Title == "" {
			t.Errorf("tool %q should have a Title annotation", name)
		}
		// Every tool must explicitly set ReadOnlyHint and DestructiveHint
		if ann.ReadOnlyHint == nil {
			t.Errorf("tool %q should have ReadOnlyHint set", name)
		}
		if ann.DestructiveHint == nil {
			t.Errorf("tool %q should have DestructiveHint set", name)
		}
	}
}

func TestReadOnlyToolAnnotations(t *testing.T) {
	s := newMCPTestStore(t)
	srv := NewServer(s)
	tools := srv.ListTools()

	readOnlyTools := []string{
		"mem_search", "mem_context",
		"mem_suggest_topic_key", "mem_stats", "mem_timeline",
	}
	for _, name := range readOnlyTools {
		tool := tools[name]
		if tool == nil {
			continue
		}
		ann := tool.Tool.Annotations
		if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
			t.Errorf("tool %q should be marked readOnly", name)
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Errorf("tool %q should NOT be marked destructive", name)
		}
	}
}

// ─── Issue #25: Session collision regression tests ──────────────────────────

func TestDefaultSessionIDScopedByProject(t *testing.T) {
	if got := defaultSessionID(""); got != "manual-save" {
		t.Fatalf("expected manual-save for empty project, got %q", got)
	}
	if got := defaultSessionID("ohara"); got != "manual-save-ohara" {
		t.Fatalf("expected manual-save-ohara, got %q", got)
	}
	if got := defaultSessionID("my-app"); got != "manual-save-my-app" {
		t.Fatalf("expected manual-save-my-app, got %q", got)
	}
}

func TestHandleSaveCreatesProjectScopedSession(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	// Save from project A without session_id
	reqA := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Decision A",
		"content": "Architecture for project A",
		"type":    "architecture",
		"project": "projectA",
	}}}
	resA, err := h(context.Background(), reqA)
	if err != nil || resA.IsError {
		t.Fatalf("save A: err=%v isError=%v", err, resA.IsError)
	}

	// Save from project B without session_id
	reqB := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Decision B",
		"content": "Architecture for project B",
		"type":    "architecture",
		"project": "projectB",
	}}}
	resB, err := h(context.Background(), reqB)
	if err != nil || resB.IsError {
		t.Fatalf("save B: err=%v isError=%v", err, resB.IsError)
	}

	// Verify separate sessions exist for each project
	// Note: project names are normalized to lowercase, so projectA → projecta
	sessA, err := s.GetSession("manual-save-projecta")
	if err != nil {
		t.Fatalf("expected session manual-save-projecta to exist: %v", err)
	}
	if sessA.Project != "projecta" {
		t.Fatalf("expected project=projecta (normalized), got %q", sessA.Project)
	}

	sessB, err := s.GetSession("manual-save-projectb")
	if err != nil {
		t.Fatalf("expected session manual-save-projectb to exist: %v", err)
	}
	if sessB.Project != "projectb" {
		t.Fatalf("expected project=projectb (normalized), got %q", sessB.Project)
	}
}

func TestHandleSavePromptCreatesProjectScopedSession(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSavePrompt(s, MCPConfig{})

	reqA := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "How do I set up auth?",
		"project": "alpha",
	}}}
	resA, err := h(context.Background(), reqA)
	if err != nil || resA.IsError {
		t.Fatalf("save prompt A: err=%v isError=%v", err, resA.IsError)
	}

	reqB := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "How do I deploy?",
		"project": "beta",
	}}}
	resB, err := h(context.Background(), reqB)
	if err != nil || resB.IsError {
		t.Fatalf("save prompt B: err=%v isError=%v", err, resB.IsError)
	}

	if _, err := s.GetSession("manual-save-alpha"); err != nil {
		t.Fatalf("expected session manual-save-alpha: %v", err)
	}
	if _, err := s.GetSession("manual-save-beta"); err != nil {
		t.Fatalf("expected session manual-save-beta: %v", err)
	}
}

func TestHandleSessionSummaryCreatesProjectScopedSession(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSessionSummary(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "Worked on auth module",
		"project": "gamma",
	}}}
	res, err := h(context.Background(), req)
	if err != nil || res.IsError {
		t.Fatalf("session summary: err=%v isError=%v", err, res.IsError)
	}

	if _, err := s.GetSession("manual-save-gamma"); err != nil {
		t.Fatalf("expected session manual-save-gamma: %v", err)
	}
}

func TestHandleCapturePassiveCreatesProjectScopedSession(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleCapturePassive(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"content": "## Key Learnings:\nAuth needs rate limiting",
		"project": "delta",
	}}}
	res, err := h(context.Background(), req)
	if err != nil || res.IsError {
		t.Fatalf("capture passive: err=%v isError=%v text=%s", err, res.IsError, callResultText(t, res))
	}

	if _, err := s.GetSession("manual-save-delta"); err != nil {
		t.Fatalf("expected session manual-save-delta: %v", err)
	}
}

func TestExplicitSessionIDBypassesDefault(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	// Provide explicit session_id — should NOT use defaultSessionID
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":      "Explicit session test",
		"content":    "Testing explicit session ID",
		"type":       "discovery",
		"project":    "myproject",
		"session_id": "custom-session-123",
	}}}
	res, err := h(context.Background(), req)
	if err != nil || res.IsError {
		t.Fatalf("save: err=%v isError=%v", err, res.IsError)
	}

	// Should use the explicit session, NOT "manual-save-myproject"
	if _, err := s.GetSession("custom-session-123"); err != nil {
		t.Fatalf("expected custom-session-123: %v", err)
	}
	// The default session should NOT exist
	_, err = s.GetSession("manual-save-myproject")
	if err == nil {
		t.Fatal("manual-save-myproject should NOT exist when explicit session_id provided")
	}
}

func TestDestructiveToolAnnotation(t *testing.T) {
	s := newMCPTestStore(t)
	srv := NewServer(s)
	tools := srv.ListTools()

	tool := tools["mem_delete"]
	if tool == nil {
		t.Fatal("mem_delete should be registered")
	}
	ann := tool.Tool.Annotations
	if ann.DestructiveHint == nil || !*ann.DestructiveHint {
		t.Error("mem_delete should be marked destructive")
	}
	if ann.ReadOnlyHint == nil || *ann.ReadOnlyHint {
		t.Error("mem_delete should NOT be marked readOnly")
	}
}

// ─── Phase 3: MCPConfig, Default Project, Normalization, Similar Warnings ────

func TestNewServerWithConfig(t *testing.T) {
	s := newMCPTestStore(t)
	cfg := MCPConfig{DefaultProject: "ohara"}
	srv := NewServerWithConfig(s, cfg, nil)
	if srv == nil {
		t.Fatal("expected MCP server instance")
	}
	tools := srv.ListTools()
	// Should have all 30 tools
	if len(tools) != 30 {
		t.Errorf("NewServerWithConfig should register all 30 tools, got %d", len(tools))
	}
}

func TestHandleSaveDefaultProjectFillIn(t *testing.T) {
	s := newMCPTestStore(t)
	cfg := MCPConfig{DefaultProject: "myproject"}
	h := handleSave(s, cfg, NewSessionActivity(10*time.Minute))

	// Send empty project — should use default
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Test memory",
		"content": "Some content here",
		// no project field
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", callResultText(t, res))
	}

	// Verify memory was stored with default project
	mems, err := s.GetMemories("myproject", "project", "", store.MemoryStatusActive, 5)
	if err != nil {
		t.Fatalf("get memories: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("expected at least one memory stored with default project")
	}
	if mems[0].ProjectID != "myproject" {
		t.Fatalf("expected projectID=myproject, got %v", mems[0].ProjectID)
	}
}

func TestHandleSaveNormalizationWarning(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	// Send mixed-case project name — should be normalized and warning returned
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Normalization test",
		"content": "Testing project name normalization",
		"project": "Ohara", // uppercase — should normalize to "ohara"
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "normalized") {
		t.Fatalf("expected normalization warning in response, got %q", text)
	}

	// Verify memory was stored with normalized project name
	mems, err := s.GetMemories("ohara", "project", "", store.MemoryStatusActive, 5)
	if err != nil {
		t.Fatalf("get memories: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("expected memory stored under normalized project name 'ohara'")
	}
}

func TestHandleSaveSimilarProjectWarning(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	// First save to "ohara" to establish an existing project
	req1 := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "First memory",
		"content": "Memory for ohara project",
		"project": "ohara",
	}}}
	res1, err := h(context.Background(), req1)
	if err != nil || res1.IsError {
		t.Fatalf("first save: err=%v isError=%v text=%s", err, res1.IsError, callResultText(t, res1))
	}

	// Now save to "ahara" (typo) — should warn about similar project "ohara"
	req2 := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Typo project memory",
		"content": "Memory saved under typo project name",
		"project": "ahara", // typo — Levenshtein distance 1 from "ohara"
	}}}
	res2, err := h(context.Background(), req2)
	if err != nil {
		t.Fatalf("second save handler error: %v", err)
	}
	if res2.IsError {
		t.Fatalf("unexpected error on second save: %s", callResultText(t, res2))
	}

	text := callResultText(t, res2)
	if !strings.Contains(text, "Similar project") {
		t.Fatalf("expected similar project warning, got %q", text)
	}
	// Verify spec-compliant format: ⚠️ emoji, obs count, and "Consider using"
	if !strings.Contains(text, "⚠️") {
		t.Errorf("expected ⚠️ emoji in warning, got %q", text)
	}
	if !strings.Contains(text, "memories") {
		t.Errorf("expected observation count (memories) in warning, got %q", text)
	}
	if !strings.Contains(text, "Consider using") {
		t.Errorf("expected 'Consider using' in warning, got %q", text)
	}
}

func TestHandleSaveNoSimilarWarningWhenProjectExists(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	// Save twice to the same project — second save should NOT show similar project warning
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "First memory",
		"content": "Memory content",
		"project": "ohara",
	}}}
	h(context.Background(), req) // first save establishes the project

	req2 := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Second memory",
		"content": "Another memory content",
		"project": "ohara",
	}}}
	res2, err := h(context.Background(), req2)
	if err != nil || res2.IsError {
		t.Fatalf("second save: err=%v", err)
	}

	text := callResultText(t, res2)
	if strings.Contains(text, "Similar project") {
		t.Fatalf("unexpected similar project warning on existing project, got %q", text)
	}
}

func TestHandleSaveConflictWarningSurfacesWithoutBlocking(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	// Seed an existing decision memory (via AddMemory so it's in memory_items table)
	_, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Attempt to save a conflicting decision memory — should succeed with warning
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Auth decision: JWT for session management",
		"content": "Alternative approach",
		"type":    "decision",
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	// Save should succeed (not an error response)
	if res.IsError {
		t.Fatalf("expected successful save despite conflict, got error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	// Should surface conflict warning
	if !strings.Contains(text, "Conflict detected") {
		t.Fatalf("expected conflict warning in response, got: %s", text)
	}
	if !strings.Contains(text, "similarity:") {
		t.Fatalf("expected similarity score in conflict warning, got: %s", text)
	}
	if !strings.Contains(text, "mem_update") {
		t.Fatalf("expected suggestion to use mem_update in conflict warning, got: %s", text)
	}
}

func TestHandleSaveNoConflictWarningForNonConflictKinds(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleSave(s, MCPConfig{}, NewSessionActivity(10*time.Minute))

	// Seed an existing decision memory
	_, err := s.AddMemory(store.AddMemoryParams{
		ProjectID: "ohara",
		Kind:      store.MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	// Save a bugfix (not decision/pattern/config) — should NOT show conflict warning
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Auth decision: JWT for session management",
		"content": "Fixed a bug in auth",
		"type":    "bugfix",
		"project": "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if strings.Contains(text, "Conflict detected") {
		t.Fatalf("unexpected conflict warning for bugfix type, got: %s", text)
	}
}

func TestHandleMergeProjects(t *testing.T) {
	s := newMCPTestStore(t)

	// Set up memory items under different project name variants
	if err := s.CreateSession("s-Ohara", "Ohara", ""); err != nil {
		t.Fatalf("create session Ohara: %v", err)
	}
	if _, err := s.AddMemory(store.AddMemoryParams{
		SessionID: "s-Ohara",
		Kind:      "decision",
		Title:     "From Ohara",
		Body:      "Content from Ohara",
		ProjectID: "ohara", // store normalizes to lowercase
	}); err != nil {
		t.Fatalf("add memory Ohara: %v", err)
	}

	if err := s.CreateSession("s-ohara-memory", "ohara-memory", ""); err != nil {
		t.Fatalf("create session ohara-memory: %v", err)
	}
	if _, err := s.AddMemory(store.AddMemoryParams{
		SessionID: "s-ohara-memory",
		Kind:      "decision",
		Title:     "From ohara-memory",
		Body:      "Content from ohara-memory",
		ProjectID: "ohara-memory",
	}); err != nil {
		t.Fatalf("add memory ohara-memory: %v", err)
	}

	h := handleMergeProjects(s)

	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"from": "ohara-memory, OHARA", // comma-separated, with spaces and uppercase
		"to":   "ohara",
	}}}

	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", callResultText(t, res))
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "ohara") {
		t.Fatalf("expected merge result mentioning canonical project, got %q", text)
	}
	if !strings.Contains(text, "Observations moved") {
		t.Fatalf("expected Observations moved in result, got %q", text)
	}

	// Verify that ohara-memory memories are now under "ohara"
	mems, err := s.GetMemories("ohara", "", "", store.MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("get memories: %v", err)
	}
	// Should have both: original "ohara" mem + migrated "ohara-memory" mem
	if len(mems) < 2 {
		t.Fatalf("expected at least 2 memories after merge, got %d", len(mems))
	}
}

func TestHandleMergeProjectsRequiresFromAndTo(t *testing.T) {
	s := newMCPTestStore(t)
	h := handleMergeProjects(s)

	// Missing "from"
	res, err := h(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"to": "ohara",
	}}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when 'from' is missing")
	}

	// Missing "to"
	res, err = h(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"from": "ohara-old",
	}}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when 'to' is missing")
	}

	// Empty from after parsing
	res, err = h(context.Background(), mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"from": "  , , ",
		"to":   "ohara",
	}}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when all 'from' values are empty after trimming")
	}
}

func TestHandleMergeProjectsIsInAdminProfile(t *testing.T) {
	s := newMCPTestStore(t)
	allowlist := ResolveTools("admin")
	srv := NewServerWithTools(s, allowlist)
	tools := srv.ListTools()

	if tools["mem_merge_projects"] == nil {
		t.Fatal("mem_merge_projects should be in admin profile")
	}

	// Verify it's marked destructive
	tool := tools["mem_merge_projects"]
	ann := tool.Tool.Annotations
	if ann.DestructiveHint == nil || !*ann.DestructiveHint {
		t.Error("mem_merge_projects should be marked destructive")
	}
}

func TestHandleMergeProjectsIsDeferred(t *testing.T) {
	s := newMCPTestStore(t)
	srv := NewServer(s)
	tools := srv.ListTools()

	tool := tools["mem_merge_projects"]
	if tool == nil {
		t.Fatal("mem_merge_projects should be registered")
	}
	if !tool.Tool.DeferLoading {
		t.Error("mem_merge_projects should have DeferLoading=true")
	}
}

func TestHandleSaveDefaultProjectDoesNotOverrideExplicit(t *testing.T) {
	s := newMCPTestStore(t)
	cfg := MCPConfig{DefaultProject: "default-project"}
	h := handleSave(s, cfg, NewSessionActivity(10*time.Minute))

	// Explicit project should override default
	req := mcppkg.CallToolRequest{Params: mcppkg.CallToolParams{Arguments: map[string]any{
		"title":   "Explicit project test",
		"content": "Should go to explicit-project, not default-project",
		"project": "explicit-project",
	}}}
	res, err := h(context.Background(), req)
	if err != nil || res.IsError {
		t.Fatalf("save: err=%v", err)
	}

	// Verify it went to explicit-project, NOT default-project
	obs, err := s.GetMemories("explicit-project", "project", "", store.MemoryStatusActive, 5)
	if err != nil || len(obs) == 0 {
		t.Fatal("expected observation in explicit-project")
	}
	defaultObs, err := s.GetMemories("default-project", "project", "", store.MemoryStatusActive, 5)
	if err != nil {
		t.Fatalf("lookup default-project: %v", err)
	}
	if len(defaultObs) > 0 {
		t.Fatal("observation should NOT be in default-project")
	}
}

func TestSearchResponseIncludesNudgeAfterInactivity(t *testing.T) {
	s := newMCPTestStore(t)

	// Seed a memory to search for
	s.CreateSession("s1", "myproject", "")
	s.AddMemory(store.AddMemoryParams{
		SessionID: "s1",
		Kind:      store.MemoryKindDecision,
		Title:     "test memory",
		Body:      "some content",
		ProjectID: "myproject",
		Scope:     "project",
	})

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	activity := NewSessionActivity(10 * time.Minute)
	activity.now = func() time.Time { return now }

	sessionID := defaultSessionID("myproject")

	// Simulate prior activity: > 5 tool calls so nudge criteria is met
	for i := 0; i < 6; i++ {
		activity.RecordToolCall(sessionID)
	}

	// Advance time past nudge threshold
	now = now.Add(15 * time.Minute)

	search := handleSearch(s, MCPConfig{}, activity)
	res, err := search(context.Background(), mcppkg.CallToolRequest{
		Params: mcppkg.CallToolParams{Arguments: map[string]any{
			"query":   "test memory",
			"project": "myproject",
		}},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "No mem_save calls for this project") {
		t.Fatalf("expected nudge in search response, got: %q", text)
	}
}

func TestSessionSummaryResponseIncludesActivityScore(t *testing.T) {
	s := newMCPTestStore(t)

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	activity := NewSessionActivity(10 * time.Minute)
	activity.now = func() time.Time { return now }

	// Use defaultSessionID so we test the real wiring — session summary
	// looks up activity via defaultSessionID(project), not an explicit session_id.
	project := "myproject"
	sessionID := defaultSessionID(project)

	// Simulate activity
	for i := 0; i < 12; i++ {
		activity.RecordToolCall(sessionID)
	}
	activity.RecordSave(sessionID)
	activity.RecordSave(sessionID)

	summary := handleSessionSummary(s, MCPConfig{}, activity)
	res, err := summary(context.Background(), mcppkg.CallToolRequest{
		Params: mcppkg.CallToolParams{Arguments: map[string]any{
			"project": project,
			"content": "## Goal\nTest session",
		}},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := callResultText(t, res)
	if !strings.Contains(text, "Session activity:") {
		t.Fatalf("expected activity score in session summary response, got: %q", text)
	}
	if !strings.Contains(text, "12 tool calls") {
		t.Fatalf("expected 12 tool calls in score, got: %q", text)
	}
	if !strings.Contains(text, "2 saves") {
		t.Fatalf("expected 2 saves in score, got: %q", text)
	}
}

func TestSessionEndClearsActivity(t *testing.T) {
	s := newMCPTestStore(t)

	activity := NewSessionActivity(10 * time.Minute)
	project := "myproject"
	sessionID := defaultSessionID(project)

	// Record some activity
	activity.RecordToolCall(sessionID)
	activity.RecordSave(sessionID)

	// Verify activity exists
	score := activity.ActivityScore(sessionID)
	if score == "" {
		t.Fatal("expected activity score before session end")
	}

	// Create session in store so EndSession works
	s.CreateSession("real-session-id", project, "")

	end := handleSessionEnd(s, MCPConfig{DefaultProject: project}, activity)
	_, err := end(context.Background(), mcppkg.CallToolRequest{
		Params: mcppkg.CallToolParams{Arguments: map[string]any{
			"id": "real-session-id",
		}},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Activity should be cleared
	score = activity.ActivityScore(sessionID)
	if score != "" {
		t.Fatalf("expected empty activity after session end, got: %q", score)
	}
}

func TestCapturePassiveRecordsToolCall(t *testing.T) {
	s := newMCPTestStore(t)

	activity := NewSessionActivity(10 * time.Minute)
	project := "myproject"
	sessionID := defaultSessionID(project)

	capture := handleCapturePassive(s, MCPConfig{DefaultProject: project}, activity)
	_, err := capture(context.Background(), mcppkg.CallToolRequest{
		Params: mcppkg.CallToolParams{Arguments: map[string]any{
			"content": "## Key Learnings:\n1. Test learning",
			"project": project,
		}},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Verify tool call was recorded
	score := activity.ActivityScore(sessionID)
	if !strings.Contains(score, "1 tool call") {
		t.Fatalf("expected 1 tool call recorded for capture passive, got: %q", score)
	}
}

func TestSessionStartUsesDefaultSessionID(t *testing.T) {
	s := newMCPTestStore(t)

	activity := NewSessionActivity(10 * time.Minute)
	project := "myproject"

	start := handleSessionStart(s, MCPConfig{}, activity)
	_, err := start(context.Background(), mcppkg.CallToolRequest{
		Params: mcppkg.CallToolParams{Arguments: map[string]any{
			"id":      "real-unique-session-id",
			"project": project,
		}},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Activity should be recorded under defaultSessionID, not the real session ID
	defaultSID := defaultSessionID(project)
	score := activity.ActivityScore(defaultSID)
	if !strings.Contains(score, "1 tool call") {
		t.Fatalf("expected activity under defaultSessionID, got: %q", score)
	}

	// The real session ID should NOT have activity
	realScore := activity.ActivityScore("real-unique-session-id")
	if realScore != "" {
		t.Fatalf("expected no activity under real session ID, got: %q", realScore)
	}
}

func TestSessionStartCandidateNotice(t *testing.T) {
	s := newMCPTestStore(t)

	// Create 3 observational memories in the same project+domain+kind group
	// to trigger consolidation candidate generation.
	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           store.MemoryKindDiscovery,
			Title:          fmt.Sprintf("Obs item %d", i),
			Body:           fmt.Sprintf("Body of observation %d", i),
			Classification: "observational",
			Domain:         "test",
		})
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
	}

	// Generate consolidation candidate.
	created, _, err := s.GenerateConsolidationCandidates("ohara", "test", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected 1 candidate, got %d", created)
	}

	// Call handleSessionStart and verify the candidate notice is present.
	activity := NewSessionActivity(10 * time.Minute)
	start := handleSessionStart(s, MCPConfig{}, activity)
	res, err := start(context.Background(), mcppkg.CallToolRequest{
		Params: mcppkg.CallToolParams{Arguments: map[string]any{
			"id":      "test-session",
			"project": "ohara",
		}},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", callResultText(t, res))
	}
	text := callResultText(t, res)
	if !strings.Contains(text, "1 consolidation candidate(s) are ready for review") {
		t.Fatalf("expected candidate notice in session start result, got: %s", text)
	}
}

func TestConsolidateCandidatesReturnsSourceEpisodes(t *testing.T) {
	s := newMCPTestStore(t)

	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(store.AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           store.MemoryKindDiscovery,
			Title:          fmt.Sprintf("Source memory %d", i),
			Body:           fmt.Sprintf("raw episodic detail %d", i),
			Classification: "observational",
			Domain:         "test",
		})
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
	}

	created, _, err := s.GenerateConsolidationCandidates("ohara", "test", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected 1 candidate, got %d", created)
	}

	h := handleConsolidationCandidates(s)
	res, err := h(context.Background(), mcppkg.CallToolRequest{
		Params: mcppkg.CallToolParams{Arguments: map[string]any{"project": "ohara", "domain": "test"}},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected handler error: %s", callResultText(t, res))
	}
	text := callResultText(t, res)
	if !strings.Contains(text, "Source memories (3)") {
		t.Fatalf("expected grouped source count in output, got: %s", text)
	}
	if !strings.Contains(text, "Source memory 1") || !strings.Contains(text, "raw episodic detail 1") {
		t.Fatalf("expected source episode content in output, got: %s", text)
	}
	if !strings.Contains(text, "Save a semantic memory with mem_save using source='consolidation'") {
		t.Fatalf("expected consolidation workflow guidance, got: %s", text)
	}
}
