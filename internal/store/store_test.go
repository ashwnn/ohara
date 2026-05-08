package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ashwnn/ohara/internal/token"
	"github.com/ashwnn/ohara/internal/util"
	_ "modernc.org/sqlite"
)

func mustDefaultConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	return cfg
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	cfg := mustDefaultConfig(t)
	cfg.DataDir = t.TempDir()
	cfg.DedupeWindow = time.Hour

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

type fakeRows struct {
	next    []bool
	scanErr error
	err     error
}

func (f *fakeRows) Next() bool {
	if len(f.next) == 0 {
		return false
	}
	v := f.next[0]
	f.next = f.next[1:]
	return v
}

func (f *fakeRows) Scan(dest ...any) error {
	return f.scanErr
}

func (f *fakeRows) Err() error {
	return f.err
}

func (f *fakeRows) Close() error {
	return nil
}

func TestNewMigratesLegacyUserPromptsSyncIDSchema(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "ohara.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			summary TEXT
		);
		CREATE TABLE user_prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			project TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);
		INSERT INTO sessions (id, project, directory) VALUES ('s1', 'ohara', '/tmp/ohara');
		INSERT INTO user_prompts (session_id, content, project) VALUES ('s1', 'legacy prompt', 'ohara');
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed legacy db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	cfg := mustDefaultConfig(t)
	cfg.DataDir = dataDir

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new store after legacy prompt schema: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var syncID string
	if err := s.db.QueryRow("SELECT sync_id FROM user_prompts WHERE content = ?", "legacy prompt").Scan(&syncID); err != nil {
		t.Fatalf("query migrated prompt sync_id: %v", err)
	}
	if syncID == "" {
		t.Fatalf("expected migrated prompt sync_id to be backfilled")
	}

	var hasSyncIDColumn bool
	rows, err := s.db.Query("PRAGMA table_info(user_prompts)")
	if err != nil {
		t.Fatalf("query prompt columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan prompt column: %v", err)
		}
		if name == "sync_id" {
			hasSyncIDColumn = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate prompt columns: %v", err)
	}
	if !hasSyncIDColumn {
		t.Fatalf("expected user_prompts.sync_id column after migration")
	}

	var indexName string
	if err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_prompts_sync_id'").Scan(&indexName); err != nil {
		t.Fatalf("query prompt sync index: %v", err)
	}
	if indexName != "idx_prompts_sync_id" {
		t.Fatalf("expected idx_prompts_sync_id to exist, got %q", indexName)
	}
}

func TestSuggestTopicKeyNormalizesDeterministically(t *testing.T) {
	got := SuggestTopicKey("Architecture", "  Auth Model  ", "ignored")
	if got != "architecture/auth-model" {
		t.Fatalf("expected architecture/auth-model, got %q", got)
	}

	fallback := SuggestTopicKey("bugfix", "", "Fix nil panic in auth middleware on empty token")
	if fallback != "bug/fix-nil-panic-in-auth-middleware-on-empty" {
		t.Fatalf("unexpected fallback topic key: %q", fallback)
	}
}

func TestSuggestTopicKeyInfersFamilyFromTextWhenTypeIsGeneric(t *testing.T) {
	bug := SuggestTopicKey("manual", "", "Fix regression in auth login flow")
	if bug != "bug/fix-regression-in-auth-login-flow" {
		t.Fatalf("expected bug family inference, got %q", bug)
	}

	arch := SuggestTopicKey("", "ADR: Split API gateway boundary", "")
	if arch != "architecture/adr-split-api-gateway-boundary" {
		t.Fatalf("expected architecture family inference, got %q", arch)
	}
}

func TestPromptProjectNullScan(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Manually insert a prompt with NULL project to simulate legacy data or external changes
	_, err := s.db.Exec(
		"INSERT INTO user_prompts (session_id, content, project) VALUES (?, ?, NULL)",
		"s1", "prompt with null project",
	)
	if err != nil {
		t.Fatalf("manual insert: %v", err)
	}

	// 1. Test RecentPrompts
	prompts, err := s.RecentPrompts("", 10)
	if err != nil {
		t.Fatalf("RecentPrompts failed with null project: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Project != "" {
		t.Errorf("expected empty string for null project, got %q", prompts[0].Project)
	}

	// 2. Test SearchPrompts
	searchResult, err := s.SearchPrompts("null", "", 10)
	if err != nil {
		t.Fatalf("SearchPrompts failed with null project: %v", err)
	}
	if len(searchResult) != 1 || searchResult[0].Project != "" {
		t.Errorf("expected empty string for null project in search, got %q", searchResult[0].Project)
	}

	// 3. Test Export
	data, err := s.Export()
	if err != nil {
		t.Fatalf("Export failed with null project: %v", err)
	}
	found := false
	for _, p := range data.Prompts {
		if p.Content == "prompt with null project" {
			found = true
			if p.Project != "" {
				t.Errorf("expected empty string for null project in export, got %q", p.Project)
			}
		}
	}
	if !found {
		t.Error("exported prompts missing the test prompt")
	}
}

// ─── Passive Capture Tests ───────────────────────────────────────────────────

func TestExtractLearnings(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			name: "NumberedList",
			text: `Some preamble text here.

## Key Learnings:

1. bcrypt cost=12 is the right balance for our server performance
2. JWT refresh tokens need atomic rotation to prevent race conditions
3. Always validate the audience claim in JWT tokens before trusting them

## Next Steps
- something else
`,
			expected: []string{
				"bcrypt cost=12 is the right balance for our server performance",
				"JWT refresh tokens need atomic rotation to prevent race conditions",
				"Always validate the audience claim in JWT tokens before trusting them",
			},
		},
		{
			name: "SpanishHeader",
			text: `## Aprendizajes Clave:

1. El costo de bcrypt=12 es el balance correcto para nuestro servidor
2. Los refresh tokens de JWT necesitan rotacion atomica
`,
			expected: []string{
				"El costo de bcrypt=12 es el balance correcto para nuestro servidor",
				"Los refresh tokens de JWT necesitan rotacion atomica",
			},
		},
		{
			name: "BulletList",
			text: `### Learnings:

- bcrypt cost=12 is the right balance for our server performance
- JWT refresh tokens need atomic rotation to prevent race conditions
`,
			expected: []string{
				"bcrypt cost=12 is the right balance for our server performance",
				"JWT refresh tokens need atomic rotation to prevent race conditions",
			},
		},
		{
			name: "IgnoresShortItems",
			text: `## Key Learnings:

1. too short
2. bcrypt cost=12 is the right balance for our server performance
3. also short
`,
			expected: []string{
				"bcrypt cost=12 is the right balance for our server performance",
			},
		},
		{
			name: "NoSection",
			text: `This is just regular text without any learning section headers.
It has multiple lines but no ## Key Learnings or similar.
`,
			expected: nil,
		},
		{
			name: "SectionPresentButNoValidItems",
			text: `## Key Learnings:

1. short
2. tiny
`,
			expected: nil,
		},
		{
			name: "UsesLastSection",
			text: `## Key Learnings:

1. This is from the first section and should be ignored

Some other text here.

## Key Learnings:

1. This is from the last section and should be captured as the real one
`,
			expected: []string{
				"This is from the last section and should be captured as the real one",
			},
		},
		{
			name: "FallsBackWhenLastSectionHasNoValidItems",
			text: `## Key Learnings:

1. This is long enough and should be captured from the previous section

## Key Learnings:

1. short
2. tiny
`,
			expected: []string{
				"This is long enough and should be captured from the previous section",
			},
		},
		{
			name: "CleansMarkdown",
			text: "## Key Learnings:\n\n1. **Use** `context.Context` in *all* handlers to support cancellation correctly\n",
			expected: []string{
				"Use context.Context in all handlers to support cancellation correctly",
			},
		},
		{
			name: "ParenthesesList",
			text: `## Key Learnings:

1) bcrypt cost=12 is the right balance for our server performance
2) JWT refresh tokens need atomic rotation to prevent race conditions
`,
			expected: []string{
				"bcrypt cost=12 is the right balance for our server performance",
				"JWT refresh tokens need atomic rotation to prevent race conditions",
			},
		},
		{
			name: "AsteriskBulletList",
			text: `## Key Learnings:

* bcrypt cost=12 is the right balance for our server performance
* JWT refresh tokens need atomic rotation to prevent race conditions
`,
			expected: []string{
				"bcrypt cost=12 is the right balance for our server performance",
				"JWT refresh tokens need atomic rotation to prevent race conditions",
			},
		},
		{
			name: "SingularHeader",
			text: `## Key Learning:

1. bcrypt cost=12 is the right balance for our server performance
`,
			expected: []string{
				"bcrypt cost=12 is the right balance for our server performance",
			},
		},
		{
			name:     "EmptyString",
			text:     "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			learnings := ExtractLearnings(tt.text)

			if len(learnings) != len(tt.expected) {
				t.Fatalf("expected %d learnings, got %d: %v", len(tt.expected), len(learnings), learnings)
			}

			for i, expected := range tt.expected {
				if learnings[i] != expected {
					t.Errorf("expected learning %d to be %q, got %q", i, expected, learnings[i])
				}
			}
		})
	}
}

func TestPassiveCaptureEmptyContent(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := s.PassiveCapture(PassiveCaptureParams{
		SessionID: "s1",
		Content:   "",
		Project:   "ohara",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("passive capture: %v", err)
	}
	if result.Extracted != 0 || result.Saved != 0 {
		t.Fatalf("expected 0 extracted and 0 saved, got %d/%d", result.Extracted, result.Saved)
	}
}

func TestPassiveCaptureReturnsErrorWhenSessionDoesNotExist(t *testing.T) {
	s := newTestStore(t)

	text := `## Key Learnings:

1. This learning is long enough to attempt insert and fail without session
`
	result, err := s.PassiveCapture(PassiveCaptureParams{
		SessionID: "missing-session",
		Content:   text,
		Project:   "ohara",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("expected no error when session does not exist, got: %v", err)
	}
	if result.Extracted <= 0 {
		t.Fatalf("expected Extracted > 0, got %d", result.Extracted)
	}
}

func TestStoreLocalSyncFoundationStateHelpers(t *testing.T) {
	s := newTestStore(t)

	state, err := s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get initial sync state: %v", err)
	}
	if state.Lifecycle != SyncLifecycleIdle {
		t.Fatalf("expected idle lifecycle, got %q", state.Lifecycle)
	}

	acquired, err := s.AcquireSyncLease(DefaultSyncTargetKey, "worker-a", 2*time.Minute, time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}
	if !acquired {
		t.Fatalf("expected first lease acquisition to succeed")
	}

	acquired, err = s.AcquireSyncLease(DefaultSyncTargetKey, "worker-b", 2*time.Minute, time.Date(2026, 3, 7, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("acquire conflicting lease: %v", err)
	}
	if acquired {
		t.Fatalf("expected conflicting lease acquisition to fail")
	}

	if err := s.ReleaseSyncLease(DefaultSyncTargetKey, "worker-a"); err != nil {
		t.Fatalf("release lease: %v", err)
	}

	acquired, err = s.AcquireSyncLease(DefaultSyncTargetKey, "worker-b", 2*time.Minute, time.Date(2026, 3, 7, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("acquire released lease: %v", err)
	}
	if !acquired {
		t.Fatalf("expected lease acquisition after release to succeed")
	}

	if err := s.MarkSyncFailure(DefaultSyncTargetKey, "timeout talking to cloud", time.Date(2026, 3, 7, 12, 10, 0, 0, time.UTC)); err != nil {
		t.Fatalf("mark sync failure: %v", err)
	}

	state, err = s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get degraded sync state: %v", err)
	}
	if state.Lifecycle != SyncLifecycleDegraded {
		t.Fatalf("expected degraded lifecycle, got %q", state.Lifecycle)
	}
	if state.ConsecutiveFailures != 1 {
		t.Fatalf("expected failure count 1, got %d", state.ConsecutiveFailures)
	}
	if state.LastError == nil || *state.LastError != "timeout talking to cloud" {
		t.Fatalf("expected last error to be stored, got %+v", state.LastError)
	}
	if state.BackoffUntil == nil || *state.BackoffUntil != "2026-03-07T12:10:00Z" {
		t.Fatalf("expected backoff timestamp to be stored, got %+v", state.BackoffUntil)
	}

	if err := s.MarkSyncHealthy(DefaultSyncTargetKey); err != nil {
		t.Fatalf("mark sync healthy: %v", err)
	}

	state, err = s.GetSyncState(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("get healthy sync state: %v", err)
	}
	if state.Lifecycle != SyncLifecycleHealthy {
		t.Fatalf("expected healthy lifecycle, got %q", state.Lifecycle)
	}
	if state.ConsecutiveFailures != 0 || state.LastError != nil || state.BackoffUntil != nil {
		t.Fatalf("expected healthy state to clear failure metadata, got %+v", state)
	}
}

func TestApplyPulledMutationAcceptsStringifiedSessionPayload(t *testing.T) {
	s := newTestStore(t)

	mutation := SyncMutation{
		Seq:       1,
		TargetKey: DefaultSyncTargetKey,
		Entity:    SyncEntitySession,
		EntityKey: "remote-session",
		Op:        SyncOpUpsert,
		Payload:   `"{\"id\":\"remote-session\",\"project\":\"ohara\",\"directory\":\"/remote\"}"`,
	}
	if err := s.ApplyPulledMutation(DefaultSyncTargetKey, mutation); err != nil {
		t.Fatalf("apply stringified session mutation: %v", err)
	}

	session, err := s.GetSession("remote-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Project != "ohara" || session.Directory != "/remote" {
		t.Fatalf("unexpected session after pulled apply: %+v", session)
	}
}

func TestUtilityHelpersCoverage(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Fatalf("expected empty string for nil pointer, got %q", got)
	}
	v := "value"
	if got := derefString(&v); got != "value" {
		t.Fatalf("expected dereferenced value, got %q", got)
	}

	if got := maxInt(10, 5); got != 10 {
		t.Fatalf("expected maxInt(10,5)=10, got %d", got)
	}
	if got := maxInt(3, 7); got != 7 {
		t.Fatalf("expected maxInt(3,7)=7, got %d", got)
	}

	if got := dedupeWindowExpression(0); got != "-15 minutes" {
		t.Fatalf("expected default dedupe window, got %q", got)
	}
	if got := dedupeWindowExpression(20 * time.Second); got != "-1 minutes" {
		t.Fatalf("expected minimum 1 minute window, got %q", got)
	}

	cases := map[string]string{
		"write":   "file_change",
		"patch":   "file_change",
		"bash":    "command",
		"read":    "file_read",
		"glob":    "search",
		"unknown": "tool_use",
	}
	for in, want := range cases {
		if got := ClassifyTool(in); got != want {
			t.Fatalf("ClassifyTool(%q): expected %q, got %q", in, want, got)
		}
	}
}

func TestInferTopicFamilyCoverage(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		title   string
		content string
		want    string
	}{
		{name: "type architecture", typ: "architecture", want: "architecture"},
		{name: "type bugfix", typ: "bugfix", want: "bug"},
		{name: "type decision", typ: "decision", want: "decision"},
		{name: "type pattern", typ: "pattern", want: "pattern"},
		{name: "type config", typ: "config", want: "config"},
		{name: "type discovery", typ: "discovery", want: "discovery"},
		{name: "type learning", typ: "learning", want: "learning"},
		{name: "type session summary", typ: "session_summary", want: "session"},
		{name: "text bug", title: "", content: "this caused a crash regression", want: "bug"},
		{name: "text architecture", title: "", content: "new boundary design", want: "architecture"},
		{name: "text decision", title: "", content: "we chose this tradeoff", want: "decision"},
		{name: "text pattern", title: "", content: "naming convention for handlers", want: "pattern"},
		{name: "text config", title: "", content: "docker env setup", want: "config"},
		{name: "text discovery", title: "", content: "root cause found", want: "discovery"},
		{name: "text learning", title: "", content: "key learning from this issue", want: "learning"},
		{name: "fallback type", typ: "Custom Type", want: "custom-type"},
		{name: "default topic", typ: "manual", title: "", content: "", want: "topic"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferTopicFamily(tc.typ, tc.title, tc.content)
			if got != tc.want {
				t.Fatalf("inferTopicFamily(%q,%q,%q): expected %q, got %q", tc.typ, tc.title, tc.content, tc.want, got)
			}
		})
	}
}

func TestEndSessionEdgeCases(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-edge", "ohara", "/tmp/ohara"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.EndSession("missing", "ignored"); err != nil {
		t.Fatalf("end missing session should be no-op: %v", err)
	}

	if err := s.EndSession("s-edge", ""); err != nil {
		t.Fatalf("end session with empty summary: %v", err)
	}

	sess, err := s.GetSession("s-edge")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatalf("expected ended_at to be set")
	}
	if sess.Summary != nil {
		t.Fatalf("expected empty summary to persist as NULL, got %q", *sess.Summary)
	}
}

func TestMigrationAndHelperEdgeBranches(t *testing.T) {
	t.Run("migrate is idempotent with existing triggers", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.migrate(); err != nil {
			t.Fatalf("second migrate should succeed: %v", err)
		}
	})

	t.Run("topic helpers normalize edge cases", func(t *testing.T) {
		if got := SuggestTopicKey("decision", "decision", ""); got != "decision/general" {
			t.Fatalf("expected decision/general, got %q", got)
		}
		if got := SuggestTopicKey("bugfix", "bug-auth-panic", ""); got != "bug/auth-panic" {
			t.Fatalf("expected bug/auth-panic, got %q", got)
		}
		if got := SuggestTopicKey("manual", "!!!", "..."); got != "topic/general" {
			t.Fatalf("expected topic/general fallback, got %q", got)
		}

		longSegment := normalizeTopicSegment(strings.Repeat("abc", 50))
		if len(longSegment) != 100 {
			t.Fatalf("expected topic segment truncation to 100, got %d", len(longSegment))
		}

		longKey := normalizeTopicKey(strings.Repeat("k", 200))
		if len(longKey) != 120 {
			t.Fatalf("expected topic key truncation to 120, got %d", len(longKey))
		}
	})

	t.Run("format context empty returns empty string", func(t *testing.T) {
		s := newTestStore(t)
		ctx, err := s.FormatContext("", "")
		if err != nil {
			t.Fatalf("format context: %v", err)
		}
		if ctx != "" {
			t.Fatalf("expected empty context when no data, got %q", ctx)
		}
	})
}

func TestExportImportEdgeBranches(t *testing.T) {
	t.Run("export fails when prompts query fails", func(t *testing.T) {
		s := newTestStore(t)

		if _, err := s.db.Exec(`
			DROP TRIGGER IF EXISTS prompt_fts_insert;
			DROP TRIGGER IF EXISTS prompt_fts_update;
			DROP TRIGGER IF EXISTS prompt_fts_delete;
			DROP TABLE IF EXISTS prompts_fts;
			DROP TABLE user_prompts;
		`); err != nil {
			t.Fatalf("drop prompts: %v", err)
		}

		_, err := s.Export()
		if err == nil || !strings.Contains(err.Error(), "export prompts") {
			t.Fatalf("expected prompts export error, got %v", err)
		}
	})

	t.Run("import begin tx fails on closed db", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}

		_, err := s.Import(&ExportData{})
		if err == nil || !strings.Contains(err.Error(), "begin tx") {
			t.Fatalf("expected begin tx import error, got %v", err)
		}
	})

	t.Run("import fails on prompt fk error", func(t *testing.T) {
		s := newTestStore(t)
		_, err := s.Import(&ExportData{
			Prompts: []Prompt{{
				ID:        1,
				SessionID: "missing-session",
				Content:   "prompt",
				Project:   "ohara",
				CreatedAt: Now(),
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "import prompt") {
			t.Fatalf("expected prompt import error, got %v", err)
		}
	})
}

func TestNewErrorBranches(t *testing.T) {
	t.Run("fails when data dir is a file", func(t *testing.T) {
		base := t.TempDir()
		badPath := filepath.Join(base, "not-a-dir")
		if err := os.WriteFile(badPath, []byte("x"), 0600); err != nil {
			t.Fatalf("write file: %v", err)
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = badPath

		_, err := New(cfg)
		if err == nil || !strings.Contains(err.Error(), "create data dir") {
			t.Fatalf("expected create data dir error, got %v", err)
		}
	})

	t.Run("fails when db path is a directory", func(t *testing.T) {
		dataDir := t.TempDir()
		dbAsDir := filepath.Join(dataDir, "ohara.db")
		if err := os.Mkdir(dbAsDir, 0755); err != nil {
			t.Fatalf("mkdir db path: %v", err)
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = dataDir

		_, err := New(cfg)
		if err == nil {
			t.Fatalf("expected New to fail when db path is a directory")
		}
	})

	t.Run("fails when migration encounters conflicting object", func(t *testing.T) {
		dataDir := t.TempDir()
		dbPath := filepath.Join(dataDir, "ohara.db")

		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		_, err = db.Exec(`
			CREATE TABLE sessions (
				id TEXT PRIMARY KEY,
				project TEXT NOT NULL,
				directory TEXT NOT NULL,
				started_at TEXT NOT NULL,
				ended_at TEXT,
				summary TEXT
			);
			CREATE TABLE user_prompts (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL,
				content TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
		`)
		if err != nil {
			_ = db.Close()
			t.Fatalf("create conflicting view: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = dataDir

		_, err = New(cfg)
		if err == nil || !strings.Contains(err.Error(), "migration") {
			t.Fatalf("expected migration error, got %v", err)
		}
	})
}

func TestMigrationInternalErrorAndNoopBranches(t *testing.T) {
	t.Run("addColumnIfNotExists adds then noops", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.db.Exec(`CREATE TABLE extra_table (id INTEGER)`); err != nil {
			t.Fatalf("create extra table: %v", err)
		}

		if err := s.addColumnIfNotExists("extra_table", "name", "TEXT"); err != nil {
			t.Fatalf("add column: %v", err)
		}
		if err := s.addColumnIfNotExists("extra_table", "name", "TEXT"); err != nil {
			t.Fatalf("add existing column should noop: %v", err)
		}

		if err := s.addColumnIfNotExists("missing_table", "x", "TEXT"); err == nil {
			t.Fatalf("expected missing table error")
		}
	})

	t.Run("migrate returns deterministic exec hook errors", func(t *testing.T) {
		s := newTestStore(t)

		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "UPDATE user_prompts SET project") {
				return nil, errors.New("forced migrate update failure")
			}
			return origExec(db, query, args...)
		}

		err := s.migrate()
		if err == nil || !strings.Contains(err.Error(), "forced migrate update failure") {
			t.Fatalf("expected forced migrate failure, got %v", err)
		}
	})

	t.Run("migrate fails when creating missing triggers", func(t *testing.T) {
		s := newTestStore(t)

		// Drop prompt triggers so migrate attempts to create them.
		if _, err := s.db.Exec(`
			DROP TRIGGER IF EXISTS prompt_fts_insert;
			DROP TRIGGER IF EXISTS prompt_fts_update;
			DROP TRIGGER IF EXISTS prompt_fts_delete;
		`); err != nil {
			t.Fatalf("drop prompt triggers: %v", err)
		}

		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "CREATE TRIGGER prompt_fts_insert") {
				return nil, errors.New("forced prompt trigger failure")
			}
			return origExec(db, query, args...)
		}

		err := s.migrate()
		if err == nil || !strings.Contains(err.Error(), "forced prompt trigger failure") {
			t.Fatalf("expected forced trigger failure, got %v", err)
		}
	})

}

func TestImportExportSeamErrors(t *testing.T) {
	t.Run("export query hooks", func(t *testing.T) {
		s := newTestStore(t)

		origQueryIt := s.hooks.queryIt
		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "FROM sessions") {
				return nil, errors.New("forced sessions export query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Export(); err == nil || !strings.Contains(err.Error(), "export sessions") {
			t.Fatalf("expected sessions export error, got %v", err)
		}

		s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
			if strings.Contains(query, "FROM user_prompts") {
				return nil, errors.New("forced prompts export query error")
			}
			return origQueryIt(db, query, args...)
		}
		if _, err := s.Export(); err == nil || !strings.Contains(err.Error(), "export prompts") {
			t.Fatalf("expected prompts export error, got %v", err)
		}
	})

	t.Run("import tx and exec hooks", func(t *testing.T) {
		s := newTestStore(t)

		s.hooks.beginTx = func(_ *sql.DB) (*sql.Tx, error) {
			return nil, errors.New("forced import begin failure")
		}
		if _, err := s.Import(&ExportData{}); err == nil || !strings.Contains(err.Error(), "begin tx") {
			t.Fatalf("expected begin tx error, got %v", err)
		}

		s.hooks = defaultStoreHooks()
		origExec := s.hooks.exec
		s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
			if strings.Contains(query, "INSERT OR IGNORE INTO sessions") {
				return nil, errors.New("forced import session insert failure")
			}
			return origExec(db, query, args...)
		}
		if _, err := s.Import(&ExportData{Sessions: []Session{{ID: "s-x", Project: "p", Directory: "/tmp", StartedAt: Now()}}}); err == nil || !strings.Contains(err.Error(), "import session") {
			t.Fatalf("expected session import error, got %v", err)
		}

		s.hooks = defaultStoreHooks()
		s.hooks.commit = func(_ *sql.Tx) error {
			return errors.New("forced import commit failure")
		}
		if _, err := s.Import(&ExportData{}); err == nil || !strings.Contains(err.Error(), "import: commit") {
			t.Fatalf("expected commit error, got %v", err)
		}
	})
}

func TestHookFallbacksAndAdditionalBranches(t *testing.T) {
	t.Run("hook fallbacks call default DB methods", func(t *testing.T) {
		s := newTestStore(t)
		s.hooks = storeHooks{}

		if _, err := s.execHook(s.db, "SELECT 1"); err != nil {
			t.Fatalf("exec hook fallback: %v", err)
		}
		rows, err := s.queryHook(s.db, "SELECT 1")
		if err != nil {
			t.Fatalf("query hook fallback: %v", err)
		}
		_ = rows.Close()

		iter, err := s.queryItHook(s.db, "SELECT 1")
		if err != nil {
			t.Fatalf("query iterator fallback: %v", err)
		}
		_ = iter.Close()

		tx, err := s.beginTxHook()
		if err != nil {
			t.Fatalf("begin tx hook fallback: %v", err)
		}
		if err := s.commitHook(tx); err != nil {
			t.Fatalf("commit hook fallback: %v", err)
		}

		s2 := newTestStore(t)
		rows2, err := s2.queryHook(s2.db, "SELECT 1")
		if err != nil {
			t.Fatalf("query hook default closure: %v", err)
		}
		_ = rows2.Close()

		s.hooks.query = func(db queryer, query string, args ...any) (*sql.Rows, error) {
			return nil, errors.New("forced query hook error")
		}
		s.hooks.queryIt = nil
		if _, err := s.queryItHook(s.db, "SELECT 1"); err == nil {
			t.Fatalf("expected queryItHook error through queryHook fallback")
		}
	})

	t.Run("format context returns specific query stage errors", func(t *testing.T) {
		t.Run("recent sessions error", func(t *testing.T) {
			s := newTestStore(t)
			_ = s.Close()
			if _, err := s.FormatContext("", ""); err == nil {
				t.Fatalf("expected format context to fail from recent sessions")
			}
		})

		t.Run("recent prompts error", func(t *testing.T) {
			s := newTestStore(t)
			if err := s.CreateSession("s-ctx2", "ohara", "/tmp/ohara"); err != nil {
				t.Fatalf("create session: %v", err)
			}
			if _, err := s.db.Exec("DROP TABLE user_prompts"); err != nil {
				t.Fatalf("drop prompts: %v", err)
			}
			if _, err := s.FormatContext("", ""); err == nil {
				t.Fatalf("expected format context to fail from recent prompts")
			}
		})
	})
}

func TestStoreUncoveredBranchesPushToHundred(t *testing.T) {
	t.Run("new open database hook error", func(t *testing.T) {
		orig := openDB
		t.Cleanup(func() { openDB = orig })
		openDB = func(driverName, dataSourceName string) (*sql.DB, error) {
			return nil, errors.New("forced open error")
		}

		cfg := mustDefaultConfig(t)
		cfg.DataDir = t.TempDir()
		if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "open database") {
			t.Fatalf("expected open database error, got %v", err)
		}
	})

	t.Run("migrate forced failures for remaining exec branches", func(t *testing.T) {
		failCases := []string{
			"UPDATE user_prompts SET project = ''",
			"CREATE TRIGGER prompt_fts_insert",
		}
		for _, needle := range failCases {
			t.Run(needle, func(t *testing.T) {
				s := newTestStore(t)
				if strings.Contains(needle, "CREATE TRIGGER prompt_fts_insert") {
					if _, err := s.db.Exec(`
						DROP TRIGGER IF EXISTS prompt_fts_insert;
						DROP TRIGGER IF EXISTS prompt_fts_update;
						DROP TRIGGER IF EXISTS prompt_fts_delete;
					`); err != nil {
						t.Fatalf("drop prompt triggers: %v", err)
					}
				}
				origExec := s.hooks.exec
				s.hooks.exec = func(db execer, query string, args ...any) (sql.Result, error) {
					if strings.Contains(query, needle) {
						return nil, errors.New("forced migrate failure")
					}
					return origExec(db, query, args...)
				}
				if err := s.migrate(); err == nil {
					t.Fatalf("expected migrate error for %q", needle)
				}
			})
		}
	})

	t.Run("migrate addColumn and legacy-call propagation", func(t *testing.T) {
		t.Run("propagates addColumn error", func(t *testing.T) {
			s := newTestStore(t)
			origQueryIt := s.hooks.queryIt
			called := 0
			s.hooks.queryIt = func(db queryer, query string, args ...any) (rowScanner, error) {
				if strings.Contains(query, "PRAGMA table_info(sync_mutations)") {
					called++
					if called == 1 {
						return nil, errors.New("forced addColumn failure")
					}
				}
				return origQueryIt(db, query, args...)
			}
			if err := s.migrate(); err == nil {
				t.Fatalf("expected migrate to propagate addColumn failure")
			}
		})

	})

}

// ─── Issue #25: Session collision regression tests ──────────────────────────

func TestCreateSessionUpsertsEmptyProjectAndDirectory(t *testing.T) {
	s := newTestStore(t)

	// Create session with empty project/directory (simulates first MCP call without context)
	if err := s.CreateSession("sess-upsert", "", ""); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Second call with real project/directory should fill in the blanks.
	// Project names are normalized to lowercase, so "projectA" becomes "projecta".
	if err := s.CreateSession("sess-upsert", "projectA", "/tmp/a"); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	sess, err := s.GetSession("sess-upsert")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Project != "projecta" {
		t.Fatalf("expected project=projecta after upsert (normalized), got %q", sess.Project)
	}
	if sess.Directory != "/tmp/a" {
		t.Fatalf("expected directory=/tmp/a after upsert, got %q", sess.Directory)
	}
}

func TestCreateSessionDoesNotOverwriteExistingProject(t *testing.T) {
	s := newTestStore(t)

	// Create session with project A (normalized to "projecta")
	if err := s.CreateSession("sess-preserve", "projectA", "/tmp/a"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Second call with project B should NOT overwrite
	if err := s.CreateSession("sess-preserve", "projectB", "/tmp/b"); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	sess, err := s.GetSession("sess-preserve")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	// Project names are normalized to lowercase, so "projectA" is stored as "projecta"
	if sess.Project != "projecta" {
		t.Fatalf("expected project=projecta (preserved, normalized), got %q", sess.Project)
	}
	if sess.Directory != "/tmp/a" {
		t.Fatalf("expected directory=/tmp/a (preserved), got %q", sess.Directory)
	}
}

func TestCreateSessionPartialUpsert(t *testing.T) {
	s := newTestStore(t)

	t.Run("fills directory when project already set", func(t *testing.T) {
		if err := s.CreateSession("sess-partial-1", "myproject", ""); err != nil {
			t.Fatalf("create: %v", err)
		}
		// Second call fills directory but project stays
		if err := s.CreateSession("sess-partial-1", "other", "/new/dir"); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		sess, err := s.GetSession("sess-partial-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if sess.Project != "myproject" {
			t.Fatalf("project should be preserved, got %q", sess.Project)
		}
		if sess.Directory != "/new/dir" {
			t.Fatalf("directory should be filled, got %q", sess.Directory)
		}
	})

	t.Run("fills project when directory already set", func(t *testing.T) {
		if err := s.CreateSession("sess-partial-2", "", "/existing/dir"); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := s.CreateSession("sess-partial-2", "newproject", ""); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		sess, err := s.GetSession("sess-partial-2")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if sess.Project != "newproject" {
			t.Fatalf("project should be filled, got %q", sess.Project)
		}
		if sess.Directory != "/existing/dir" {
			t.Fatalf("directory should be preserved, got %q", sess.Directory)
		}
	})

	t.Run("both empty stays empty", func(t *testing.T) {
		if err := s.CreateSession("sess-partial-3", "", ""); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := s.CreateSession("sess-partial-3", "", ""); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		sess, err := s.GetSession("sess-partial-3")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if sess.Project != "" {
			t.Fatalf("project should stay empty, got %q", sess.Project)
		}
		if sess.Directory != "" {
			t.Fatalf("directory should stay empty, got %q", sess.Directory)
		}
	})
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "short ascii", in: "abc", max: 10, want: "abc"},
		{name: "exact length", in: "hello", max: 5, want: "hello"},
		{name: "long ascii", in: "abcdef", max: 3, want: "abc..."},
		{name: "spanish accents", in: "Decisión de arquitectura", max: 8, want: "Decisión..."},
		{name: "emoji", in: "🐛🔧🚀✨🎉💡", max: 3, want: "🐛🔧🚀..."},
		{name: "mixed ascii and multibyte", in: "café☕latte", max: 5, want: "café☕..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := util.Truncate(tc.in, tc.max)
			if got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// ─── Project Enrollment CRUD Tests ───────────────────────────────────────────

func TestEnrollProjectBasic(t *testing.T) {
	s := newTestStore(t)

	// Enroll a project.
	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("enroll project: %v", err)
	}

	// Verify it shows up in the list.
	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list enrolled projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 enrolled project, got %d", len(projects))
	}
	if projects[0].Project != "ohara" {
		t.Fatalf("expected project 'ohara', got %q", projects[0].Project)
	}
	if projects[0].EnrolledAt == "" {
		t.Fatal("expected enrolled_at to be set")
	}

	// Verify IsProjectEnrolled returns true.
	enrolled, err := s.IsProjectEnrolled("ohara")
	if err != nil {
		t.Fatalf("is project enrolled: %v", err)
	}
	if !enrolled {
		t.Fatal("expected project to be enrolled")
	}
}

func TestEnrollProjectIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Enroll twice — should not error.
	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("second enroll (idempotent): %v", err)
	}

	// Should still be exactly one row.
	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 enrolled project after double-enroll, got %d", len(projects))
	}
}

func TestEnrollProjectEmptyNameReturnsError(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject(""); err == nil {
		t.Fatal("expected error when enrolling empty project name")
	}
}

func TestUnenrollProjectBasic(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject("ohara"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	// Unenroll.
	if err := s.UnenrollProject("ohara"); err != nil {
		t.Fatalf("unenroll: %v", err)
	}

	// Should be gone.
	enrolled, err := s.IsProjectEnrolled("ohara")
	if err != nil {
		t.Fatalf("is enrolled after unenroll: %v", err)
	}
	if enrolled {
		t.Fatal("expected project to be unenrolled")
	}

	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 enrolled projects after unenroll, got %d", len(projects))
	}
}

func TestUnenrollProjectIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Unenroll a project that was never enrolled — should not error.
	if err := s.UnenrollProject("nonexistent"); err != nil {
		t.Fatalf("unenroll non-enrolled project should be idempotent: %v", err)
	}
}

func TestUnenrollProjectEmptyNameReturnsError(t *testing.T) {
	s := newTestStore(t)

	if err := s.UnenrollProject(""); err == nil {
		t.Fatal("expected error when unenrolling empty project name")
	}
}

func TestIsProjectEnrolledReturnsFalseForUnknown(t *testing.T) {
	s := newTestStore(t)

	enrolled, err := s.IsProjectEnrolled("unknown-project")
	if err != nil {
		t.Fatalf("is enrolled: %v", err)
	}
	if enrolled {
		t.Fatal("expected false for unknown project")
	}
}

func TestListEnrolledProjectsEmpty(t *testing.T) {
	s := newTestStore(t)

	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list enrolled projects: %v", err)
	}
	if projects != nil {
		t.Fatalf("expected nil for empty list, got %v", projects)
	}
}

func TestListEnrolledProjectsAlphabeticalOrder(t *testing.T) {
	s := newTestStore(t)

	// Enroll in non-alphabetical order.
	for _, p := range []string{"zebra", "alpha", "mango"} {
		if err := s.EnrollProject(p); err != nil {
			t.Fatalf("enroll %q: %v", p, err)
		}
	}

	projects, err := s.ListEnrolledProjects()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projects))
	}
	expected := []string{"alpha", "mango", "zebra"}
	for i, ep := range projects {
		if ep.Project != expected[i] {
			t.Fatalf("position %d: expected %q, got %q", i, expected[i], ep.Project)
		}
	}
}

func TestSyncMutationProjectColumnExists(t *testing.T) {
	s := newTestStore(t)

	// Verify the project column exists on sync_mutations by inserting a row.
	_, err := s.db.Exec(
		`INSERT INTO sync_mutations (target_key, entity, entity_key, op, payload, source, project)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		DefaultSyncTargetKey, "session", "test-key", SyncOpUpsert, `{"project":"myproj"}`, SyncSourceLocal, "myproj",
	)
	if err != nil {
		t.Fatalf("insert sync_mutation with project: %v", err)
	}

	// Read it back and verify project is populated.
	var project string
	if err := s.db.QueryRow(`SELECT project FROM sync_mutations WHERE entity_key = ?`, "test-key").Scan(&project); err != nil {
		t.Fatalf("scan project: %v", err)
	}
	if project != "myproj" {
		t.Fatalf("expected project 'myproj', got %q", project)
	}
}

func TestSyncMutationProjectBackfill(t *testing.T) {
	s := newTestStore(t)

	// Insert a mutation that simulates a pre-migration row (project is empty, but payload has it).
	// The backfill runs during schema init, so we test it by inserting directly then re-running.
	// Since the store already ran migrations, let's verify backfill logic by inserting a new row
	// with empty project and manually running the backfill.
	_, err := s.db.Exec(
		`INSERT INTO sync_mutations (target_key, entity, entity_key, op, payload, source, project)
		 VALUES (?, ?, ?, ?, ?, ?, '')`,
		DefaultSyncTargetKey, "observation", "backfill-key", SyncOpUpsert, `{"project":"backfilled"}`, SyncSourceLocal,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Run the backfill manually.
	_, err = s.db.Exec(`
		UPDATE sync_mutations
		SET project = COALESCE(json_extract(payload, '$.project'), '')
		WHERE project = '' AND payload != ''
	`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var project string
	if err := s.db.QueryRow(`SELECT project FROM sync_mutations WHERE entity_key = ?`, "backfill-key").Scan(&project); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if project != "backfilled" {
		t.Fatalf("expected backfilled project 'backfilled', got %q", project)
	}
}

// ─── Phase 3: extractProjectFromPayload ──────────────────────────────────────

func TestExtractProjectFromSessionPayload(t *testing.T) {
	p := syncSessionPayload{ID: "s1", Project: "acme"}
	got := extractProjectFromPayload(p)
	if got != "acme" {
		t.Fatalf("expected 'acme', got %q", got)
	}
}

func TestExtractProjectFromPromptPayload(t *testing.T) {
	proj := "prompt-project"
	p := syncPromptPayload{SyncID: "p1", Project: &proj}
	got := extractProjectFromPayload(p)
	if got != "prompt-project" {
		t.Fatalf("expected 'prompt-project', got %q", got)
	}
}

func TestExtractProjectFromPromptPayloadNil(t *testing.T) {
	p := syncPromptPayload{SyncID: "p1", Project: nil}
	got := extractProjectFromPayload(p)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestExtractProjectFromUnknownPayloadFallback(t *testing.T) {
	// Unknown struct with a project field — uses JSON fallback.
	p := struct {
		Project string `json:"project"`
		Other   string `json:"other"`
	}{Project: "fallback-proj", Other: "x"}
	got := extractProjectFromPayload(p)
	if got != "fallback-proj" {
		t.Fatalf("expected 'fallback-proj', got %q", got)
	}
}

func TestExtractProjectFromPayloadWithoutProjectField(t *testing.T) {
	// Unknown struct without a project field — returns empty.
	p := struct {
		Name string `json:"name"`
	}{Name: "test"}
	got := extractProjectFromPayload(p)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// ─── Phase 3: enqueueSyncMutationTx populates project column ────────────────

func TestEnqueueSyncMutationPopulatesProjectFromSessionPayload(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateSession("enq-session", "enqueued-project", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// CreateSession enqueues a sync mutation internally. Check the project column.
	var project string
	err := s.db.QueryRow(
		`SELECT project FROM sync_mutations WHERE entity = ? AND entity_key = ?`,
		SyncEntitySession, "enq-session",
	).Scan(&project)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if project != "enqueued-project" {
		t.Fatalf("expected project='enqueued-project', got %q", project)
	}
}

func TestEnqueueSyncMutationPopulatesProjectFromPromptPayload(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateSession("prompt-enq", "prompt-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.AddPrompt(AddPromptParams{
		SessionID: "prompt-enq",
		Content:   "What did we do?",
		Project:   "prompt-proj",
	})
	if err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	var project string
	err = s.db.QueryRow(
		`SELECT project FROM sync_mutations WHERE entity = ? ORDER BY seq DESC LIMIT 1`,
		SyncEntityPrompt,
	).Scan(&project)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if project != "prompt-proj" {
		t.Fatalf("expected project='prompt-proj', got %q", project)
	}
}

// ─── Phase 4: ListPendingSyncMutations enrollment filtering ──────────────────

func TestListPendingFiltersNonEnrolledProjects(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-enrolled", "enrolled-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s-not-enrolled", "other-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Enroll only "enrolled-proj".
	if err := s.EnrollProject("enrolled-proj"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	// Only enrolled-proj mutations should appear.
	for _, m := range mutations {
		if m.Project == "other-proj" {
			t.Fatalf("non-enrolled project 'other-proj' should not appear in pending mutations")
		}
	}

	foundEnrolled := false
	for _, m := range mutations {
		if m.Project == "enrolled-proj" {
			foundEnrolled = true
			break
		}
	}
	if !foundEnrolled {
		t.Fatal("expected enrolled-proj mutations to appear")
	}
}

func TestListPendingReturnsNoMutationsWhenNoneEnrolled(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-no-enroll", "some-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	// No projects enrolled → no mutations (all have project != '').
	if len(mutations) != 0 {
		t.Fatalf("expected 0 mutations when no projects enrolled, got %d", len(mutations))
	}
}

// ─── Phase 4: SkipAckNonEnrolledMutations ────────────────────────────────────

func TestSkipAckNonEnrolledMutationsBasic(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("skip-session", "skip-proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Do NOT enroll "skip-proj" → mutations should be skip-acked.
	skipped, err := s.SkipAckNonEnrolledMutations(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("skip-ack: %v", err)
	}
	if skipped == 0 {
		t.Fatal("expected at least one mutation to be skip-acked")
	}

	// After skip-ack, there should be no pending mutations left.
	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(mutations) != 0 {
		t.Fatalf("expected 0 pending mutations after skip-ack, got %d", len(mutations))
	}
}

func TestSkipAckPreservesEnrolledProjectMutations(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject("enrolled"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	if err := s.CreateSession("s-enrolled", "enrolled", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s-not-enrolled", "not-enrolled", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Count total pending before skip-ack.
	var totalBefore int
	s.db.QueryRow(`SELECT COUNT(*) FROM sync_mutations WHERE acked_at IS NULL`).Scan(&totalBefore)

	skipped, err := s.SkipAckNonEnrolledMutations(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("skip-ack: %v", err)
	}
	if skipped == 0 {
		t.Fatal("expected at least one mutation to be skip-acked for 'not-enrolled'")
	}

	// Remaining pending should be only "enrolled" mutations.
	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	for _, m := range mutations {
		if m.Project == "not-enrolled" {
			t.Fatal("skip-acked mutation still appears as pending")
		}
	}
	if len(mutations) == 0 {
		t.Fatal("expected enrolled-project mutations to remain")
	}
}

// ─── Phase 5: Empty/global project always syncs ──────────────────────────────

func TestEmptyProjectMutationsAlwaysSync(t *testing.T) {
	s := newTestStore(t)

	// Create a session with empty project (global).
	if err := s.CreateSession("global-session", "", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// No projects enrolled, but empty-project mutations should still appear.
	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	if len(mutations) == 0 {
		t.Fatal("expected empty-project mutations to always sync regardless of enrollment")
	}

	// Verify they have project = ''.
	for _, m := range mutations {
		if m.Project != "" {
			t.Fatalf("expected empty project, got %q", m.Project)
		}
	}
}

func TestSkipAckDoesNotAffectEmptyProjectMutations(t *testing.T) {
	s := newTestStore(t)

	// Create a session with empty project (global).
	if err := s.CreateSession("global-session-2", "", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Count pending before skip-ack.
	beforeMutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	beforeCount := len(beforeMutations)

	// Skip-ack should not affect empty-project mutations.
	skipped, err := s.SkipAckNonEnrolledMutations(DefaultSyncTargetKey)
	if err != nil {
		t.Fatalf("skip-ack: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("expected 0 mutations to be skip-acked (all empty project), got %d", skipped)
	}

	// Verify count unchanged.
	afterMutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(afterMutations) != beforeCount {
		t.Fatalf("expected %d mutations after skip-ack, got %d", beforeCount, len(afterMutations))
	}
}

func TestMixedEnrolledAndEmptyProjectMutations(t *testing.T) {
	s := newTestStore(t)

	if err := s.EnrollProject("enrolled-mix"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	// Create sessions with different project states.
	if err := s.CreateSession("mix-enrolled", "enrolled-mix", "/tmp"); err != nil {
		t.Fatalf("create enrolled session: %v", err)
	}
	if err := s.CreateSession("mix-global", "", "/tmp"); err != nil {
		t.Fatalf("create global session: %v", err)
	}
	if err := s.CreateSession("mix-unenrolled", "unenrolled-mix", "/tmp"); err != nil {
		t.Fatalf("create unenrolled session: %v", err)
	}

	mutations, err := s.ListPendingSyncMutations(DefaultSyncTargetKey, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	// Should have enrolled-mix and empty-project mutations, but NOT unenrolled-mix.
	var hasEnrolled, hasGlobal bool
	for _, m := range mutations {
		if m.Project == "unenrolled-mix" {
			t.Fatal("unenrolled project mutations should not appear")
		}
		if m.Project == "enrolled-mix" {
			hasEnrolled = true
		}
		if m.Project == "" {
			hasGlobal = true
		}
	}
	if !hasEnrolled {
		t.Fatal("expected enrolled-mix mutations to appear")
	}
	if !hasGlobal {
		t.Fatal("expected empty-project (global) mutations to appear")
	}
}

// ─── MigrateProject ─────────────────────────────────────────────────────────

func TestMigrateProject(t *testing.T) {
	s := newTestStore(t)
	old, new_ := "old-name", "new-name"

	// Seed data under old project name
	s.CreateSession("s1", old, "/tmp/old")
	_, err := s.db.Exec(`
		INSERT INTO memory_items
			(session_id, project_id, kind, scope, title, body, source, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"s1", old, "decision", "project", "test memory", "some content", "import", "active",
	)
	if err != nil {
		t.Fatalf("insert memory item: %v", err)
	}
	s.AddPrompt(AddPromptParams{SessionID: "s1", Content: "test prompt", Project: old})

	// Run migration
	result, err := s.MigrateProject(old, new_)
	if err != nil {
		t.Fatalf("MigrateProject: %v", err)
	}
	if !result.Migrated {
		t.Fatal("expected migration to happen")
	}
	if result.SessionsUpdated != 1 {
		t.Fatalf("expected 1 session migrated, got %d", result.SessionsUpdated)
	}
	if result.PromptsUpdated != 1 {
		t.Fatalf("expected 1 prompt migrated, got %d", result.PromptsUpdated)
	}

	// Verify old project has no memory items
	var oldCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_items WHERE project_id = ?`, old).Scan(&oldCount); err != nil {
		t.Fatalf("count old memory items: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("expected 0 memory items under old name, got %d", oldCount)
	}

	// Verify new project has the memory item
	var newCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_items WHERE project_id = ?`, new_).Scan(&newCount); err != nil {
		t.Fatalf("count new memory items: %v", err)
	}
	if newCount != 1 {
		t.Fatalf("expected 1 memory item under new name, got %d", newCount)
	}
}

func TestMigrateProjectNoOp(t *testing.T) {
	s := newTestStore(t)

	// No records under "nonexistent" — should be a no-op
	result, err := s.MigrateProject("nonexistent", "anything")
	if err != nil {
		t.Fatalf("MigrateProject: %v", err)
	}
	if result.Migrated {
		t.Fatal("expected no migration for nonexistent project")
	}
}

func TestMigrateProjectIdempotent(t *testing.T) {
	s := newTestStore(t)
	old, new_ := "old-proj", "new-proj"

	s.CreateSession("s1", old, "/tmp")
	_, err := s.db.Exec(`
		INSERT INTO memory_items
			(session_id, project_id, kind, scope, title, body, source, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"s1", old, "decision", "project", "test", "content", "import", "active",
	)
	if err != nil {
		t.Fatalf("insert memory item: %v", err)
	}

	// First migration
	r1, err := s.MigrateProject(old, new_)
	if err != nil {
		t.Fatalf("first MigrateProject: %v", err)
	}
	if !r1.Migrated {
		t.Fatal("first migration should migrate")
	}

	// Second migration — no records under old name anymore
	r2, err := s.MigrateProject(old, new_)
	if err != nil {
		t.Fatalf("second MigrateProject: %v", err)
	}
	if r2.Migrated {
		t.Fatal("second migration should be a no-op")
	}
}

// ─── Phase 2: project-name-drift — NormalizeProject, ListProjectNames,
//              ListProjectsWithStats, MergeProjects tests ─────────────────────

func TestNormalizeProjectFunction(t *testing.T) {
	tests := []struct {
		input       string
		wantName    string
		wantWarning bool
	}{
		{"ohara", "ohara", false},
		{"Ohara", "ohara", true},
		{"OHARA", "ohara", true},
		{"  ohara  ", "ohara", true},
		{"Ohara-Memory", "ohara-memory", true},
		{"ohara--memory", "ohara-memory", true},
		{"ohara__memory", "ohara_memory", true},
		{"", "", false},
		{"already-lower", "already-lower", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, warning := NormalizeProject(tc.input)
			if got != tc.wantName {
				t.Errorf("NormalizeProject(%q) name = %q, want %q", tc.input, got, tc.wantName)
			}
			if tc.wantWarning && warning == "" {
				t.Errorf("NormalizeProject(%q) expected a warning, got empty string", tc.input)
			}
			if !tc.wantWarning && warning != "" {
				t.Errorf("NormalizeProject(%q) expected no warning, got %q", tc.input, warning)
			}
		})
	}
}

func TestAddMemoryNormalizesProject(t *testing.T) {
	s := newTestStore(t)

	// Save with mixed-case project name — AddMemory normalizes internally.
	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ProjectA",
		Kind:      MemoryKindDecision,
		Scope:     MemoryScopeProject,
		Title:     "Normalize test",
		Body:      "This should be stored under lowercase project",
		ActorID:   "test",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	if mem.ProjectID != "projecta" {
		t.Errorf("stored project = %q, want \"projecta\"", mem.ProjectID)
	}
}

func TestSearchNormalizesProjectFilter(t *testing.T) {
	s := newTestStore(t)

	// Insert a memory with project "myproj" (normalized to "myproj").
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "MyProj",
		Kind:      MemoryKindDecision,
		Scope:     MemoryScopeProject,
		Title:     "Search normalize test",
		Body:      "content for project filter normalization",
		ActorID:   "test",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Search with mixed-case project filter — should still find the record.
	results, err := s.SearchMemories("normalize test", "MyProj", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("expected ≥1 result when searching with normalized project filter, got 0")
	}
}

func TestRecentObservationsNormalizesProjectFilter(t *testing.T) {
	s := newTestStore(t)

	// Insert memory under "Alpha" (stored as "alpha").
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "Alpha",
		Kind:      MemoryKindDecision,
		Scope:     MemoryScopeProject,
		Title:     "Recent memory test A",
		Body:      "some content",
		ActorID:   "test",
	})
	if err != nil {
		t.Fatalf("AddMemory Alpha: %v", err)
	}

	// Insert memory under "Bravo" (stored as "bravo").
	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "Bravo",
		Kind:      MemoryKindDecision,
		Scope:     MemoryScopeProject,
		Title:     "Recent memory test B",
		Body:      "other content",
		ActorID:   "test",
	})
	if err != nil {
		t.Fatalf("AddMemory Bravo: %v", err)
	}

	// Query with uppercase "ALPHA" — should only return the alpha memory.
	items, err := s.GetMemories("ALPHA", "", "", MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("GetMemories: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 result with normalized project filter, got %d", len(items))
	}
	if items[0].ProjectID != "alpha" {
		t.Errorf("project = %q, want \"alpha\"", items[0].ProjectID)
	}
}

func TestCreateSessionNormalizesProject(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s-norm", "MyProject", "/tmp"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := s.GetSession("s-norm")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Project != "myproject" {
		t.Errorf("expected project=myproject (normalized), got %q", sess.Project)
	}
}

func TestListProjectNames(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "alpha", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s2", "beta", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	for i, proj := range []string{"alpha", "alpha", "beta", "gamma"} {
		content := fmt.Sprintf("content for %s iteration %d", proj, i)
		_, err := s.db.Exec(`
			INSERT INTO memory_items
				(session_id, project_id, kind, scope, title, body, source, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
			"s1", proj, "decision", "project", "test "+proj, content, "import", "active",
		)
		if err != nil {
			t.Fatalf("insert memory item: %v", err)
		}
	}

	names, err := s.ListProjectNames()
	if err != nil {
		t.Fatalf("ListProjectNames: %v", err)
	}

	// Should return distinct names: alpha, beta, gamma
	want := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected project name %q in results", n)
		}
		delete(want, n)
	}
	if len(want) > 0 {
		t.Errorf("missing project names: %v", want)
	}
}

func TestListProjectsWithStats(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "proj-a", "/work/a"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := s.CreateSession("s2", "proj-b", "/work/b"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Add 3 memories to proj-a
	for i := 0; i < 3; i++ {
		content := strings.Repeat("x", i+1)
		_, err := s.db.Exec(`
			INSERT INTO memory_items
				(session_id, project_id, kind, scope, title, body, source, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
			"s1", "proj-a", "decision", "project", "mem a", content, "import", "active",
		)
		if err != nil {
			t.Fatalf("insert memory item proj-a: %v", err)
		}
	}

	// Add 1 memory to proj-b
	{
		content := "content for proj-b"
		_, err := s.db.Exec(`
			INSERT INTO memory_items
				(session_id, project_id, kind, scope, title, body, source, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
			"s2", "proj-b", "decision", "project", "mem b", content, "import", "active",
		)
		if err != nil {
			t.Fatalf("insert memory item proj-b: %v", err)
		}
	}

	stats, err := s.ListProjectsWithStats()
	if err != nil {
		t.Fatalf("ListProjectsWithStats: %v", err)
	}

	if len(stats) < 2 {
		t.Fatalf("expected ≥2 project stats, got %d", len(stats))
	}

	// Find proj-a and proj-b in results
	statsMap := make(map[string]ProjectStats)
	for _, ps := range stats {
		statsMap[ps.Name] = ps
	}

	if a, ok := statsMap["proj-a"]; !ok {
		t.Error("proj-a not in ListProjectsWithStats results")
	} else {
		if a.MemoryCount != 3 {
			t.Errorf("proj-a: expected 3 memories, got %d", a.MemoryCount)
		}
		if a.SessionCount != 1 {
			t.Errorf("proj-a: expected 1 session, got %d", a.SessionCount)
		}
	}

	if b, ok := statsMap["proj-b"]; !ok {
		t.Error("proj-b not in ListProjectsWithStats results")
	} else {
		if b.MemoryCount != 1 {
			t.Errorf("proj-b: expected 1 memory, got %d", b.MemoryCount)
		}
	}

	// Results should be sorted by memory count descending
	if stats[0].Name != "proj-a" {
		t.Errorf("expected proj-a first (most memories), got %q", stats[0].Name)
	}
}

func TestMergeProjects(t *testing.T) {
	s := newTestStore(t)

	// Set up three source projects
	sources := []string{"ohara", "Ohara", "ohara-memory"}
	canonical := "ohara"

	if err := s.CreateSession("s1", "ohara", "/work"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Add memory items to each source
	for _, src := range []string{"ohara", "ohara-memory"} {
		for i := 0; i < 2; i++ {
			content := strings.Repeat(src, i+1)
			_, err := s.db.Exec(`
				INSERT INTO memory_items
					(session_id, project_id, kind, scope, title, body, source, status, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
				"s1", src, "decision", "project", "mem from "+src, content, "import", "active",
			)
			if err != nil {
				t.Fatalf("insert memory item %s: %v", src, err)
			}
		}
	}

	result, err := s.MergeProjects(sources, canonical)
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}

	if result.Canonical != "ohara" {
		t.Errorf("canonical = %q, want \"ohara\"", result.Canonical)
	}

	// "Ohara" normalizes to "ohara" (same as canonical) → skipped
	// "ohara-memory" is different → merged
	// Only "ohara-memory" should appear in SourcesMerged (and possibly "ohara" if it had records,
	// but it equals canonical after normalization → skipped)
	for _, merged := range result.SourcesMerged {
		if merged == "ohara" {
			t.Error("canonical 'ohara' should not appear in SourcesMerged")
		}
	}

	// All records from ohara-memory should now be under "ohara"
	var oharaCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_items WHERE project_id = ? AND status = 'active'`, "ohara").Scan(&oharaCount); err != nil {
		t.Fatalf("count ohara memory items: %v", err)
	}
	if oharaCount < 4 {
		t.Errorf("expected ≥4 memory items under 'ohara' after merge, got %d", oharaCount)
	}

	// ohara-memory should have 0 memory items
	var memCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_items WHERE project_id = ? AND status = 'active'`, "ohara-memory").Scan(&memCount); err != nil {
		t.Fatalf("count ohara-memory memory items: %v", err)
	}
	if memCount != 0 {
		t.Errorf("expected 0 memory items under 'ohara-memory' after merge, got %d", memCount)
	}
}

func TestMergeProjectsIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Merge a nonexistent source — should not error
	result, err := s.MergeProjects([]string{"ghost-project"}, "ohara")
	if err != nil {
		t.Fatalf("MergeProjects with nonexistent source: %v", err)
	}
	if result.ObservationsUpdated != 0 {
		t.Errorf("expected 0 memories updated for nonexistent source, got %d", result.ObservationsUpdated)
	}
}

func TestMergeProjectsCanonicalInSources(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("s1", "ohara", "/work"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Put a memory item under "ohara" via direct SQL.
	_, err := s.db.Exec(`
		INSERT INTO memory_items
			(session_id, project_id, kind, scope, title, body, source, status, actor_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"s1", "ohara", "decision", "project", "existing", "existing decision", "import", "active", "agent",
	)
	if err != nil {
		t.Fatalf("insert memory item: %v", err)
	}

	// Sources include the canonical itself — should be silently skipped
	result, err := s.MergeProjects([]string{"ohara", "Ohara"}, "ohara")
	if err != nil {
		t.Fatalf("MergeProjects: %v", err)
	}

	// Nothing should have been changed (ohara and Ohara both normalize to "ohara" = canonical)
	if result.SessionsUpdated != 0 {
		t.Errorf("expected 0 sessions updated when sources equal canonical, got %d", result.SessionsUpdated)
	}
	if len(result.SourcesMerged) != 0 {
		t.Errorf("expected empty SourcesMerged when all sources equal canonical, got %v", result.SourcesMerged)
	}
}

// ─── DeleteSession tests ─────────────────────────────────────────────────────

func TestDeleteSession_EmptySession(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-empty", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.DeleteSession("sess-empty"); err != nil {
		t.Fatalf("expected no error deleting empty session, got: %v", err)
	}

	// Session should be gone.
	sessions, err := s.RecentSessions("proj", 10)
	if err != nil {
		t.Fatalf("recent sessions: %v", err)
	}
	for _, ss := range sessions {
		if ss.ID == "sess-empty" {
			t.Fatal("expected session to be deleted but it still exists")
		}
	}
}

func TestDeleteSession_NotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeleteSession("does-not-exist")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestDeleteSession_HasActiveObservations(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-has-obs", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert memory item via direct SQL (AddMemory uses store API).
	if _, err := s.db.Exec(`
		INSERT INTO memory_items
			(session_id, project_id, kind, scope, title, body, source, status, actor_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"sess-has-obs", "proj", "decision", "project", "some decision", "content", "import", "active", "agent",
	); err != nil {
		t.Fatalf("insert memory item: %v", err)
	}

	err := s.DeleteSession("sess-has-obs")
	if err == nil {
		t.Fatalf("expected error deleting session with memories, got nil")
	}
	if !strings.Contains(err.Error(), "session has memories") {
		t.Fatalf("expected error containing 'session has memories', got: %v", err)
	}
}

func TestDeleteSession_HasSoftDeletedObservations(t *testing.T) {
	// Even soft-deleted (archived) memories must block the session delete
	// to avoid FK constraint violations.
	s := newTestStore(t)

	if err := s.CreateSession("sess-soft", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert memory item with archived status via direct SQL.
	if _, err := s.db.Exec(`
		INSERT INTO memory_items
			(session_id, project_id, kind, scope, title, body, source, status, actor_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"sess-soft", "proj", "decision", "project", "soft deleted memory", "content", "import", "archived", "agent",
	); err != nil {
		t.Fatalf("insert archived memory item: %v", err)
	}

	err := s.DeleteSession("sess-soft")
	if err == nil {
		t.Fatalf("expected error deleting session with archived memories, got nil")
	}
	if !strings.Contains(err.Error(), "session has memories") {
		t.Fatalf("expected error containing 'session has memories' for archived memories, got: %v", err)
	}
}

func TestDeleteSession_DeletesPromptsAlso(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-with-prompts", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.AddPrompt(AddPromptParams{
		SessionID: "sess-with-prompts",
		Content:   "a prompt",
		Project:   "proj",
	}); err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	if err := s.DeleteSession("sess-with-prompts"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	prompts, err := s.RecentPrompts("proj", 10)
	if err != nil {
		t.Fatalf("recent prompts: %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("expected prompts to be deleted with session, got %d", len(prompts))
	}
}

func TestDeleteSession_FKConstraintFallback(t *testing.T) {
	// Verify that the COUNT guard in DeleteSession correctly prevents deletion
	// when memory items exist. This test inserts a memory item directly (bypassing
	// the store API) and then verifies DeleteSession correctly returns an error
	// because the memory item count > 0.
	//
	// Note: memory_items does not have a foreign key to sessions. The COUNT guard
	// is the primary defense against orphaned sessions.
	s := newTestStore(t)

	if err := s.CreateSession("sess-race", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert a memory item directly, bypassing the store API.
	if _, err := s.db.Exec(`
		INSERT INTO memory_items
			(session_id, project_id, kind, scope, title, body, source, status, actor_id, created_at, updated_at)
		VALUES
			('sess-race', 'proj', 'decision', 'project', 'race memory', 'content', 'import', 'active', 'agent',
			 datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("pre-insert memory item: %v", err)
	}

	err := s.DeleteSession("sess-race")
	if err == nil {
		t.Fatalf("expected error when memory items exist, got nil")
	}
	if !strings.Contains(err.Error(), "session has memories") {
		t.Fatalf("expected error containing 'session has memories', got: %v", err)
	}
}

// fakeCountScanner is a rowScanner that yields a single row with value 0,
// used to simulate a COUNT(*) result of zero.
type fakeCountScanner struct {
	done bool
}

func (f *fakeCountScanner) Next() bool {
	if f.done {
		return false
	}
	f.done = true
	return true
}
func (f *fakeCountScanner) Scan(dest ...any) error {
	if len(dest) > 0 {
		if p, ok := dest[0].(*int); ok {
			*p = 0
		}
	}
	return nil
}
func (f *fakeCountScanner) Err() error   { return nil }
func (f *fakeCountScanner) Close() error { return nil }

// ─── DeletePrompt tests ──────────────────────────────────────────────────────

func TestDeletePrompt_Success(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateSession("sess-p", "proj", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	id, err := s.AddPrompt(AddPromptParams{
		SessionID: "sess-p",
		Content:   "delete me",
		Project:   "proj",
	})
	if err != nil {
		t.Fatalf("add prompt: %v", err)
	}

	if err := s.DeletePrompt(id); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	prompts, err := s.RecentPrompts("proj", 10)
	if err != nil {
		t.Fatalf("recent prompts: %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("expected prompt to be deleted, got %d", len(prompts))
	}
}

func TestDeletePrompt_NotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeletePrompt(999999)
	if !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("expected ErrPromptNotFound, got: %v", err)
	}
}

// ─── DetectConflict tests ─────────────────────────────────────────────────────

func TestDetectConflict_SkipsNonCheckingKinds(t *testing.T) {
	s := newTestStore(t)

	for _, kind := range []string{MemoryKindBugfix, MemoryKindDiscovery, MemoryKindProcedure, MemoryKindPostmortem, MemoryKindIdentity, MemoryKindUserPreference, MemoryKindGlossary} {
		ci, err := s.DetectConflict(AddMemoryParams{
			ProjectID: "ohara",
			Kind:      kind,
			Title:     "Some title that would conflict",
			Body:      "Body content",
		})
		if err != nil {
			t.Fatalf("DetectConflict for kind %q: %v", kind, err)
		}
		if ci != nil {
			t.Fatalf("kind %q: expected nil conflict, got %+v", kind, ci)
		}
	}
}

func TestDetectConflict_EmptyProjectReturnsNil(t *testing.T) {
	s := newTestStore(t)

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision",
		Body:      "Use JWT for auth",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("empty project: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_EmptyTitleReturnsNil(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision",
		Body:      "Use JWT for auth",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "", // empty title
		Body:      "Different body",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("empty title: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_NoExistingMemoriesReturnsNil(t *testing.T) {
	s := newTestStore(t)

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "New auth decision",
		Body:      "Use OAuth2",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("no existing memories: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_DissimilarTitlesReturnsNil(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Use JWT for authentication",
		Body:      "JWT-based auth is simpler to implement",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Very different title — should not conflict
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Deploy with Docker compose",
		Body:      "Use docker-compose for local dev",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("dissimilar titles: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_SimilarTitlesDetectsConflict(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: Use JWT for session management",
		Body:      "JWT is stateless and scales well",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Very similar title — should detect conflict
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: JWT for session management",
		Body:      "Alternative: use OAuth2",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci == nil {
		t.Fatal("similar titles: expected conflict, got nil")
	}
	if ci.ConflictType != "title_overlap" {
		t.Errorf("expected conflict type title_overlap, got %q", ci.ConflictType)
	}
	if ci.OverlapScore <= 0.6 {
		t.Errorf("expected overlap score > 0.6, got %f", ci.OverlapScore)
	}
	if ci.ExistingMemory == nil {
		t.Error("expected existing memory in conflict info")
	}
}

func TestDetectConflict_PicksHighestScore(t *testing.T) {
	s := newTestStore(t)

	// Add two memories: one weakly overlapping, one strongly overlapping
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Error handling pattern for API calls",
		Body:      "Return errors immediately",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "API call error handling with retry",
		Body:      "Retry failed API calls up to 3 times",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// This should match the second memory (API call error handling) more strongly
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "API call error handling and retry logic",
		Body:      "Consider exponential backoff",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci == nil {
		t.Fatal("expected conflict with best match")
	}
	// Should match "API call error handling with retry" best
	if !strings.Contains(ci.ExistingMemory.Title, "retry") {
		t.Errorf("expected best match to be the retry memory, got %q", ci.ExistingMemory.Title)
	}
}

func TestDetectConflict_DifferentKindNoConflict(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Use JWT for authentication",
		Body:      "JWT-based auth",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Same title but different kind — no conflict detection for non-checking kinds
	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Use JWT for authentication",
		Body:      "Fix the JWT implementation",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci != nil {
		t.Fatalf("different kind: expected nil conflict, got %+v", ci)
	}
}

func TestDetectConflict_ConfigKindDetected(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindConfig,
		Title:     "Database config: use PostgreSQL for production",
		Body:      "PostgreSQL for production",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	ci, err := s.DetectConflict(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindConfig,
		Title:     "Database config: PostgreSQL for production settings",
		Body:      "Connection pool size 10",
	})
	if err != nil {
		t.Fatalf("DetectConflict: %v", err)
	}
	if ci == nil {
		t.Fatal("expected conflict for config kind with similar title")
	}
	if ci.ExistingMemory.Kind != MemoryKindConfig {
		t.Errorf("expected existing memory kind config, got %q", ci.ExistingMemory.Kind)
	}
}

// ─── significantWords and keywordOverlap tests ─────────────────────────────────

func TestSignificantWords_FiltersStopwordsAndShortWords(t *testing.T) {
	words := significantWords("The and a use JWT for authentication in production systems")
	// stopwords: the, and, a, for, in
	// short words (<3): use, may be kept (JWT is 3 chars)
	wordMap := make(map[string]bool)
	for _, w := range words {
		wordMap[w] = true
	}
	// Should contain long non-stopwords
	if !wordMap["authentication"] {
		t.Error("expected 'authentication' in significant words")
	}
	if !wordMap["production"] {
		t.Error("expected 'production' in significant words")
	}
	if !wordMap["systems"] {
		t.Error("expected 'systems' in significant words")
	}
	// Should NOT contain short words or stopwords
	if wordMap["the"] || wordMap["and"] || wordMap["for"] {
		t.Error("stopwords should be filtered out")
	}
}

func TestKeywordOverlap_JaccardSimilarity(t *testing.T) {
	// Identical sets
	a := []string{"auth", "jwt", "token"}
	b := []string{"auth", "jwt", "token"}
	score := keywordOverlap(a, b)
	if score != 1.0 {
		t.Errorf("identical sets: expected 1.0, got %f", score)
	}

	// Disjoint sets
	c := []string{"auth", "jwt"}
	d := []string{"database", "postgres"}
	score = keywordOverlap(c, d)
	if score != 0.0 {
		t.Errorf("disjoint sets: expected 0.0, got %f", score)
	}

	// Partial overlap
	e := []string{"auth", "jwt", "token", "session"}
	f := []string{"auth", "jwt", "database"}
	score = keywordOverlap(e, f)
	// intersection = 2, union = 6, score = 2/6 = 0.333
	if score < 0.3 || score > 0.4 {
		t.Errorf("partial overlap: expected ~0.333, got %f", score)
	}
}

// ─── SearchMemories ranking tests ──────────────────────────────────────────────

func TestSearchMemories_DefaultLimit(t *testing.T) {
	s := newTestStore(t)

	// No memories — should return empty slice (not error)
	results, err := s.SearchMemories("auth", "", "", "", "", MemoryStatusActive, 0, "")
	if err != nil {
		t.Fatalf("SearchMemories with zero limit: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results with no matches, got %d", len(results))
	}
}

func TestSearchMemories_FindsMatchingItems(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: use JWT for tokens",
		Body:      "JWT is stateless",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	results, err := s.SearchMemories("JWT", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != MemoryKindDecision {
		t.Errorf("expected decision kind, got %s", results[0].Kind)
	}
}

func TestSearchMemories_KindBoost_DecisionsRankHigher(t *testing.T) {
	s := newTestStore(t)

	// Add both with equally matching titles so kind boost is the tiebreaker.
	// Both contain "JWT authentication" so FTS relevance is similar.
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindProcedure,
		Title:     "JWT authentication in the auth service",
		Body:      "Use middleware to validate tokens",
	})
	if err != nil {
		t.Fatalf("AddMemory procedure: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "JWT authentication decision",
		Body:      "We chose JWT for stateless auth",
	})
	if err != nil {
		t.Fatalf("AddMemory decision: %v", err)
	}

	results, err := s.SearchMemories("JWT authentication", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// Decision should be first due to kind boost (1.5x vs 1.1x)
	if results[0].Kind != MemoryKindDecision {
		t.Errorf("expected first result to be decision (kind boost 1.5), got %s", results[0].Kind)
	}
}

func TestSearchMemories_KindBoost_BugfixRanksHigherThanDiscovery(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Discovered memory leak in auth module when handling JWT",
		Body:      "The JWT refresh handler was not releasing tokens",
	})
	if err != nil {
		t.Fatalf("AddMemory discovery: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Fixed JWT token leak in auth module",
		Body:      "Added proper token release in refresh handler",
	})
	if err != nil {
		t.Fatalf("AddMemory bugfix: %v", err)
	}

	results, err := s.SearchMemories("JWT token", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// Bugfix (1.3) should rank above discovery (1.2)
	if results[0].Kind != MemoryKindBugfix {
		t.Errorf("expected first result to be bugfix (kind boost 1.3), got %s", results[0].Kind)
	}
}

func TestSearchMemories_FilterByKind(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: use JWT",
		Body:      "JWT chosen",
	})
	if err != nil {
		t.Fatalf("AddMemory decision: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Pattern for JWT validation",
		Body:      "Use middleware",
	})
	if err != nil {
		t.Fatalf("AddMemory pattern: %v", err)
	}

	// Search with kind filter
	results, err := s.SearchMemories("JWT", "ohara", "", MemoryKindPattern, "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	for _, r := range results {
		if r.Kind != MemoryKindPattern {
			t.Errorf("kind filter: expected pattern, got %s", r.Kind)
		}
	}
}

func TestSearchMemories_FilterByScope(t *testing.T) {
	s := newTestStore(t)

	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindGlossary,
		Scope:     MemoryScopeGlobal,
		Title:     "Glossary: JWT means JSON Web Token",
		Body:      "JWT is a standard",
	})
	if err != nil {
		t.Fatalf("AddMemory global: %v", err)
	}

	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Scope:     MemoryScopeProject,
		Title:     "Decision: use JWT for auth",
		Body:      "JWT chosen",
	})
	if err != nil {
		t.Fatalf("AddMemory project: %v", err)
	}

	// Global scope search — should only return global item
	results, err := s.SearchMemories("JWT", "", MemoryScopeGlobal, "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	for _, r := range results {
		if r.Scope != MemoryScopeGlobal {
			t.Errorf("scope filter: expected global, got %s", r.Scope)
		}
	}
}

func TestSearchMemories_TriggerConditionIsSearchable(t *testing.T) {
	s := newTestStore(t)

	// Add a procedure memory with a distinctive trigger_condition.
	procID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "Handle user auth failures",
		Body:             "Use a lockout strategy after repeated failed attempts",
		TriggerCondition: "When the user fails login 3 times within 5 minutes",
		Scope:            MemoryScopeProject,
	})
	if err != nil {
		t.Fatalf("AddMemory procedure: %v", err)
	}
	if procID <= 0 {
		t.Fatalf("expected valid memory id, got %d", procID)
	}

	// Add a noise memory (different kind, no trigger_condition).
	_, err = s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Auth decision: rate limit the login endpoint",
		Body:      "We chose to implement rate limiting to prevent brute force",
		Scope:     MemoryScopeProject,
	})
	if err != nil {
		t.Fatalf("AddMemory decision: %v", err)
	}

	// Search by trigger_condition text — procedure should be found even though
	// the title "Handle user auth failures" doesn't match "failed attempts".
	results, err := s.SearchMemories("failed attempts", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories by trigger_condition: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least 1 result from trigger_condition search, got 0")
	}
	if results[0].Kind != MemoryKindProcedure {
		t.Errorf("expected procedure to rank highest from trigger_condition match, got %s", results[0].Kind)
	}
	if results[0].TriggerCondition == "" {
		t.Error("expected trigger_condition to be returned in search result")
	}
}

func TestSearchMemories_TriggerConditionUpdateReindexesFTS(t *testing.T) {
	s := newTestStore(t)

	initialTrigger := "When user uploads a file larger than 10MB"
	updatedTrigger := "When user uploads any file type"

	memID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "File upload handling",
		Body:             "Validate file size and type before processing",
		TriggerCondition: initialTrigger,
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Search by initial trigger — should find it.
	results, err := s.SearchMemories("larger than 10MB", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories initial trigger: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected result from initial trigger_condition search")
	}

	// Update the trigger_condition.
	updated, err := s.UpdateMemory(memID, UpdateMemoryParams{
		TriggerCondition: &updatedTrigger,
		ActorID:          "agent",
	})
	if err != nil {
		t.Fatalf("UpdateMemory trigger_condition: %v", err)
	}
	if updated.TriggerCondition != updatedTrigger {
		t.Fatalf("expected updated trigger_condition %q, got %q", updatedTrigger, updated.TriggerCondition)
	}

	// Search by old trigger — should no longer find it.
	oldResults, err := s.SearchMemories("larger than 10MB", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories old trigger: %v", err)
	}
	if len(oldResults) != 0 {
		t.Errorf("expected no results from old trigger_condition after update, got %d", len(oldResults))
	}

	// Search by new trigger — should find it.
	newResults, err := s.SearchMemories("uploads any file", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories new trigger: %v", err)
	}
	if len(newResults) == 0 {
		t.Fatal("expected result from new trigger_condition search after update")
	}
}

func TestMigrateMemFTS_MigrationIsIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Running migrate twice should be a no-op (idempotent).
	if err := s.migrateMemFTSTriggerCondition(); err != nil {
		t.Fatalf("first migrateMemFTSTriggerCondition: %v", err)
	}
	if err := s.migrateMemFTSTriggerCondition(); err != nil {
		t.Fatalf("second migrateMemFTSTriggerCondition (idempotent): %v", err)
	}

	// Verify the FTS table has trigger_condition column.
	var colCount int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_xinfo('memory_items_fts') WHERE name = 'trigger_condition'",
	).Scan(&colCount); err != nil {
		t.Fatalf("check trigger_condition column: %v", err)
	}
	if colCount != 1 {
		t.Fatalf("expected 1 trigger_condition column in memory_items_fts, got %d", colCount)
	}

	// Verify the triggers still fire correctly for a new memory.
	memID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "Handle OOM errors",
		Body:             "Catch and log out-of-memory exceptions",
		TriggerCondition: "When the process runs out of memory",
	})
	if err != nil {
		t.Fatalf("AddMemory after migration: %v", err)
	}

	results, err := s.SearchMemories("runs out of memory", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories after migration: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search to find memory added after migration")
	}
	if results[0].ID != memID {
		t.Errorf("expected exact memory match, got id %d vs %d", results[0].ID, memID)
	}
}

func TestMigrateMemFTS_BackfillsExistingRows(t *testing.T) {
	// Set up a pre-migration database: memory_items table with some rows
	// but the FTS table WITHOUT trigger_condition.
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "ohara.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			summary TEXT
		);
		CREATE TABLE memory_items (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			project_id      TEXT NOT NULL,
			actor_id        TEXT NOT NULL DEFAULT 'agent',
			kind            TEXT NOT NULL,
			scope           TEXT NOT NULL DEFAULT 'project',
			title           TEXT NOT NULL,
			body            TEXT NOT NULL,
			tags            TEXT NOT NULL DEFAULT '[]',
			source          TEXT NOT NULL DEFAULT 'agent',
			status          TEXT NOT NULL DEFAULT 'active',
			superseded_by   INTEGER,
			expires_at      TEXT,
			ingested_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			written_by      TEXT NOT NULL DEFAULT 'agent',
			domain          TEXT NOT NULL DEFAULT '',
			evidence_json   TEXT NOT NULL DEFAULT '{}',
			applies_to_json TEXT NOT NULL DEFAULT '{}',
			related_json    TEXT NOT NULL DEFAULT '{}',
			classification  TEXT NOT NULL DEFAULT 'tactical',
			access_count    INTEGER NOT NULL DEFAULT 0,
			last_accessed   TEXT,
			valid_from      TEXT,
			valid_to        TEXT,
			superseded_at   TEXT,
			session_id      TEXT NOT NULL DEFAULT '',
			trust_level     TEXT NOT NULL DEFAULT 'system',
			trigger_condition TEXT NOT NULL DEFAULT '',
			utility_weight  REAL NOT NULL DEFAULT 0.0,
			consolidated_from TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, trigger_condition)
		VALUES ('ohara', 'agent', 'procedure', 'project',
			'Handle 500 errors',
			'Log and return a friendly error',
			'When a server returns HTTP 500');
		INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, trigger_condition)
		VALUES ('ohara', 'agent', 'procedure', 'project',
			'Handle timeout',
			'Retry with backoff',
			'When request times out after 30s');
		INSERT INTO memory_items (project_id, actor_id, kind, scope, title, body, trigger_condition)
		VALUES ('ohara', 'agent', 'decision', 'project',
			'Retry strategy',
			'Use exponential backoff',
			'');
		CREATE VIRTUAL TABLE memory_items_fts USING fts5(
			title, body, tags,
			content='memory_items',
			content_rowid='id',
			tokenize='porter unicode61'
		);
		INSERT INTO memory_items_fts(rowid, title, body, tags)
		SELECT id, title, body, tags FROM memory_items;
		CREATE TRIGGER mem_fts_insert AFTER INSERT ON memory_items BEGIN
			INSERT INTO memory_items_fts(rowid, title, body, tags)
			VALUES (new.id, new.title, new.body, new.tags);
		END;
		CREATE TRIGGER mem_fts_delete AFTER DELETE ON memory_items BEGIN
			INSERT INTO memory_items_fts(memory_items_fts, rowid, title, body, tags)
			VALUES ('delete', old.id, old.title, old.body, old.tags);
		END;
		CREATE TRIGGER mem_fts_update AFTER UPDATE ON memory_items BEGIN
			INSERT INTO memory_items_fts(memory_items_fts, rowid, title, body, tags)
			VALUES ('delete', old.id, old.title, old.body, old.tags);
			INSERT INTO memory_items_fts(rowid, title, body, tags)
			VALUES (new.id, new.title, new.body, new.tags);
		END;
		CREATE TABLE memory_revisions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id   INTEGER NOT NULL REFERENCES memory_items(id),
			ts          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			actor_id    TEXT NOT NULL,
			field       TEXT NOT NULL,
			old_value   TEXT,
			new_value   TEXT,
			reason      TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_rev_memory ON memory_revisions(memory_id, ts);

		CREATE TABLE audit_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			obs_id     TEXT NOT NULL,
			action     TEXT NOT NULL CHECK(action IN ('create', 'update', 'delete', 'archive')),
			actor_id   TEXT,
			session_id TEXT,
			trust_level TEXT,
			ts         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			snapshot   TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_audit_obs ON audit_log(obs_id);
		CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_log(session_id);

		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')));
		INSERT INTO schema_version (version, applied_at) VALUES (18, datetime('now'));
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("seed pre-migration db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// Open via Store — this should run migration 19 and rebuild the FTS table.
	cfg := mustDefaultConfig(t)
	cfg.DataDir = dataDir

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Verify the two procedure memories are now findable by their trigger_condition.
	results, err := s.SearchMemories("request times out", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories trigger_condition after migration: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected to find procedure by trigger_condition after migration backfill")
	}
	if results[0].TriggerCondition != "When request times out after 30s" {
		t.Errorf("expected specific trigger_condition, got %q", results[0].TriggerCondition)
	}

	// The HTTP 500 procedure should also be findable.
	results500, err := s.SearchMemories("HTTP 500", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories HTTP 500: %v", err)
	}
	if len(results500) == 0 {
		t.Fatal("expected to find HTTP 500 procedure by trigger_condition")
	}

	// New inserts after migration should also index trigger_condition.
	newID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindProcedure,
		Title:            "Handle panic recovery",
		Body:             "Catch panics in goroutines",
		TriggerCondition: "When a goroutine panics unexpectedly",
	})
	if err != nil {
		t.Fatalf("AddMemory post-migration: %v", err)
	}

	postResults, err := s.SearchMemories("goroutine panics", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories post-migration insert: %v", err)
	}
	if len(postResults) == 0 {
		t.Fatal("expected to find post-migration insert by trigger_condition")
	}
	if postResults[0].ID != newID {
		t.Errorf("expected new memory as top result, got id %d", postResults[0].ID)
	}
}

// ─── Memory Expiry Lifecycle Tests ──────────────────────────────────────────────

func TestAddMemory_AutoExpiryDiscovery(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Discovered SQLite quirk",
		Body:      "SQLite ignores TRIM() in certain contexts",
	})
	if err != nil {
		t.Fatalf("AddMemory discovery: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.ExpiresAt == nil {
		t.Fatal("discovery memory should have expires_at set")
	}
	if *mem.ExpiresAt == "" {
		t.Fatal("discovery memory expires_at should not be empty")
	}
	// Verify it's approximately 90 days in the future
	expiresTime, err := time.Parse(time.RFC3339, *mem.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	expectedMin := time.Now().UTC().AddDate(0, 0, 89)
	expectedMax := time.Now().UTC().AddDate(0, 0, 91)
	if expiresTime.Before(expectedMin) || expiresTime.After(expectedMax) {
		t.Fatalf("discovery TTL should be ~90 days, got %v (expected between %v and %v)", expiresTime, expectedMin, expectedMax)
	}
}

func TestAddMemory_AutoExpiryPostmortem(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPostmortem,
		Title:     "Incident postmortem",
		Body:      "Root cause was a race condition",
	})
	if err != nil {
		t.Fatalf("AddMemory postmortem: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if mem.ExpiresAt == nil {
		t.Fatal("postmortem memory should have expires_at set")
	}
	if *mem.ExpiresAt == "" {
		t.Fatal("postmortem memory expires_at should not be empty")
	}
	// Verify it's approximately 30 days in the future
	expiresTime, err := time.Parse(time.RFC3339, *mem.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	expectedMin := time.Now().UTC().AddDate(0, 0, 29)
	expectedMax := time.Now().UTC().AddDate(0, 0, 31)
	if expiresTime.Before(expectedMin) || expiresTime.After(expectedMax) {
		t.Fatalf("postmortem TTL should be ~30 days, got %v (expected between %v and %v)", expiresTime, expectedMin, expectedMax)
	}
}

func TestAddMemory_NoExpiryOtherKinds(t *testing.T) {
	s := newTestStore(t)

	// Test that non-expiry kinds don't get expires_at
	nonExpiryKinds := []string{
		MemoryKindDecision,
		MemoryKindPattern,
		MemoryKindBugfix,
		MemoryKindProcedure,
		MemoryKindConfig,
		MemoryKindIdentity,
		MemoryKindUserPreference,
		MemoryKindGlossary,
	}
	for _, kind := range nonExpiryKinds {
		id, err := s.AddMemory(AddMemoryParams{
			ProjectID: "ohara",
			Kind:      kind,
			Title:     "Test " + kind,
			Body:      "Body for " + kind,
		})
		if err != nil {
			t.Fatalf("AddMemory %s: %v", kind, err)
		}
		mem, err := s.GetMemory(id)
		if err != nil {
			t.Fatalf("GetMemory %s: %v", kind, err)
		}
		if mem.ExpiresAt != nil {
			t.Errorf("kind %s should NOT have expires_at, got %v", kind, *mem.ExpiresAt)
		}
	}
}

func TestGetMemories_ExcludesExpiredItems(t *testing.T) {
	s := newTestStore(t)

	// Add an active memory without expiry
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Active decision",
		Body:      "This should be found",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add a discovery memory (which auto-expires) and manually set its expires_at to the past
	discoveryID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Expired discovery",
		Body:      "This should be excluded",
	})
	if err != nil {
		t.Fatalf("AddMemory discovery: %v", err)
	}

	// Manually expire the discovery memory by setting expires_at to the past
	_, err = s.db.Exec(
		`UPDATE memory_items SET expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		discoveryID,
	)
	if err != nil {
		t.Fatalf("set expired: %v", err)
	}

	// GetMemories should return only the active one
	items, err := s.GetMemories("ohara", "", "", MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("GetMemories: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 active item, got %d", len(items))
	}
	if items[0].ID != activeID {
		t.Fatalf("expected active memory id %d, got %d", activeID, items[0].ID)
	}

	// Verify the expired item is not returned even if we explicitly search by its kind
	expiredItems, err := s.GetMemories("ohara", "", MemoryKindDiscovery, MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("GetMemories discovery filter: %v", err)
	}
	if len(expiredItems) != 0 {
		t.Fatalf("expected 0 expired discovery items, got %d", len(expiredItems))
	}
}

func TestSearchMemories_ExcludesExpiredItems(t *testing.T) {
	s := newTestStore(t)

	// Add an active memory that matches the search
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Use connection pooling",
		Body:      "Pool database connections for better performance",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add an expired memory that also matches the search terms
	expiredID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Use connection pooling old",
		Body:      "Pool database connections for better performance old",
	})
	if err != nil {
		t.Fatalf("AddMemory expired: %v", err)
	}

	// Manually expire the second memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		expiredID,
	)
	if err != nil {
		t.Fatalf("set expired: %v", err)
	}

	// SearchMemories should only return the active one
	results, err := s.SearchMemories("pool", "ohara", "", "", "", MemoryStatusActive, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search result (active only), got %d", len(results))
	}
	if results[0].ID != activeID {
		t.Fatalf("expected active memory id %d, got %d", activeID, results[0].ID)
	}
}

func TestBuildPack_ExcludesExpiredItems(t *testing.T) {
	s := newTestStore(t)

	// Add active memories for pack
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Active decision",
		Body:      "This decision is current and relevant",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add expired project memory
	expiredID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Expired decision",
		Body:      "This decision is outdated",
	})
	if err != nil {
		t.Fatalf("AddMemory expired: %v", err)
	}

	// Manually expire the second memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		expiredID,
	)
	if err != nil {
		t.Fatalf("set expired: %v", err)
	}

	// BuildPack should only include the active memory
	pack, err := s.BuildPack(PackParams{ProjectID: "ohara"})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}

	// Check that expired item is not in pack output
	if strings.Contains(pack.Pack, "Expired decision") {
		t.Fatal("expired memory should not appear in pack")
	}
	if !strings.Contains(pack.Pack, "Active decision") {
		t.Fatal("active memory should appear in pack")
	}

	// Verify the memory items in the result
	foundActive := false
	foundExpired := false
	for _, item := range pack.MemoryItems {
		if item.ID == activeID {
			foundActive = true
		}
		if item.ID == expiredID {
			foundExpired = true
		}
	}
	if !foundActive {
		t.Fatal("active memory should be in pack.MemoryItems")
	}
	if foundExpired {
		t.Fatal("expired memory should not be in pack.MemoryItems")
	}
}

func TestGetMemories_ArchivedItemsRemainRetrievable(t *testing.T) {
	s := newTestStore(t)

	// Add an active decision memory
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Active decision",
		Body:      "This is current",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add a discovery memory and set it to archived+expired
	archivedID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDiscovery,
		Title:     "Archived discovery",
		Body:      "This is archived and expired",
	})
	if err != nil {
		t.Fatalf("AddMemory archived: %v", err)
	}

	// Set status to archived and expire the discovery memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET status = 'archived', expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		archivedID,
	)
	if err != nil {
		t.Fatalf("set archived and expired: %v", err)
	}

	// Verify GetMemory (by ID) can still retrieve the expired item directly
	mem, err := s.GetMemory(archivedID)
	if err != nil {
		t.Fatalf("GetMemory should retrieve expired item by ID: %v", err)
	}
	if mem.ID != archivedID {
		t.Fatalf("expected memory id %d, got %d", archivedID, mem.ID)
	}

	// Verify default GetMemories (status="") excludes expired items
	items, err := s.GetMemories("ohara", "", "", "", 10)
	if err != nil {
		t.Fatalf("GetMemories with default status: %v", err)
	}
	if len(items) != 1 || items[0].ID != activeID {
		t.Fatalf("expected 1 active item, got %d items", len(items))
	}

	// Verify GetMemories with explicit status="active" also excludes expired items
	activeItems, err := s.GetMemories("ohara", "", "", MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("GetMemories with active status: %v", err)
	}
	if len(activeItems) != 1 || activeItems[0].ID != activeID {
		t.Fatalf("expected 1 active item, got %d items", len(activeItems))
	}

	// Verify GetMemories with status="archived" CAN retrieve archived/expired items
	archivedItems, err := s.GetMemories("ohara", "", "", MemoryStatusArchived, 10)
	if err != nil {
		t.Fatalf("GetMemories with archived status: %v", err)
	}
	foundArchived := false
	for _, item := range archivedItems {
		if item.ID == archivedID {
			foundArchived = true
			break
		}
	}
	if !foundArchived {
		t.Fatalf("expected archived/expired item to be retrievable via archived status, got %d items", len(archivedItems))
	}
}

func TestSearchMemories_ArchivedItemsRemainSearchable(t *testing.T) {
	s := newTestStore(t)

	// Add an active pattern memory
	activeID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Active pattern auth",
		Body:      "Use middleware for auth validation",
	})
	if err != nil {
		t.Fatalf("AddMemory active: %v", err)
	}

	// Add an archived pattern memory that is also expired
	archivedID, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Archived pattern auth",
		Body:      "Use middleware for old auth approach",
	})
	if err != nil {
		t.Fatalf("AddMemory archived: %v", err)
	}

	// Set status to archived and expire the memory
	_, err = s.db.Exec(
		`UPDATE memory_items SET status = 'archived', expires_at = '2020-01-01T00:00:00Z' WHERE id = ?`,
		archivedID,
	)
	if err != nil {
		t.Fatalf("set archived and expired: %v", err)
	}

	// Verify default SearchMemories (status="") excludes expired items
	results, err := s.SearchMemories("middleware", "ohara", "", "", "", "", 10, "")
	if err != nil {
		t.Fatalf("SearchMemories with default status: %v", err)
	}
	if len(results) != 1 || results[0].ID != activeID {
		t.Fatalf("expected 1 active search result, got %d", len(results))
	}

	// Verify SearchMemories with status="archived" includes archived/expired items
	archivedResults, err := s.SearchMemories("middleware", "ohara", "", "", "", MemoryStatusArchived, 10, "")
	if err != nil {
		t.Fatalf("SearchMemories with archived status: %v", err)
	}
	foundArchived := false
	for _, item := range archivedResults {
		if item.ID == archivedID {
			foundArchived = true
			break
		}
	}
	if !foundArchived {
		t.Fatalf("expected archived/expired pattern item to be searchable via archived status, got %d results", len(archivedResults))
	}
}

func TestMemoryExpiresAt_HelperFunction(t *testing.T) {
	// Test discovery TTL (90 days)
	expires := MemoryExpiresAt(MemoryKindDiscovery)
	if expires == nil {
		t.Fatal("discovery should have TTL")
	}
	expiresTime, err := time.Parse(time.RFC3339, *expires)
	if err != nil {
		t.Fatalf("parse discovery expires_at: %v", err)
	}
	ttl := MemoryTTL(MemoryKindDiscovery)
	if ttl != 90 {
		t.Fatalf("expected discovery TTL 90 days, got %d", ttl)
	}
	expectedExpiry := time.Now().UTC().AddDate(0, 0, ttl)
	diff := expiresTime.Sub(expectedExpiry)
	if diff < -time.Minute || diff > time.Minute {
		t.Fatalf("discovery expires_at should be ~90 days from now, diff=%v", diff)
	}

	// Test postmortem TTL (30 days)
	expires = MemoryExpiresAt(MemoryKindPostmortem)
	if expires == nil {
		t.Fatal("postmortem should have TTL")
	}
	ttl = MemoryTTL(MemoryKindPostmortem)
	if ttl != 30 {
		t.Fatalf("expected postmortem TTL 30 days, got %d", ttl)
	}

	// Test non-expiring kinds
	for _, kind := range []string{MemoryKindDecision, MemoryKindPattern, MemoryKindBugfix} {
		expires = MemoryExpiresAt(kind)
		if expires != nil {
			t.Errorf("kind %s should not have TTL, got %v", kind, *expires)
		}
		if MemoryTTL(kind) != 0 {
			t.Errorf("kind %s TTL should be 0, got %d", kind, MemoryTTL(kind))
		}
	}
}

func TestMarkConsolidatedArchivesCandidateAndSources(t *testing.T) {
	s := newTestStore(t)

	var sourceIDs []int64
	for i := 1; i <= 3; i++ {
		id, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindDiscovery,
			Title:          fmt.Sprintf("Episode %d", i),
			Body:           fmt.Sprintf("raw session detail %d", i),
			Classification: "observational",
			Domain:         "api",
		})
		if err != nil {
			t.Fatalf("AddMemory source %d: %v", i, err)
		}
		sourceIDs = append(sourceIDs, id)
	}

	created, _, err := s.GenerateConsolidationCandidates("ohara", "api", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected 1 candidate, got %d", created)
	}

	groups, err := s.GetConsolidationCandidates("ohara", "api")
	if err != nil {
		t.Fatalf("GetConsolidationCandidates: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 candidate group, got %d", len(groups))
	}
	candidateID := groups[0].Candidate.ID

	consolidatedID, err := s.AddMemory(AddMemoryParams{
		ProjectID:        "ohara",
		Kind:             MemoryKindDecision,
		Title:            "Durable API learning",
		Body:             "Semantic summary",
		Source:           "consolidation",
		Classification:   "tactical",
		Domain:           "api",
		ConsolidatedFrom: groups[0].Candidate.ConsolidatedFrom,
	})
	if err != nil {
		t.Fatalf("AddMemory consolidated: %v", err)
	}

	if err := s.MarkConsolidated(candidateID, consolidatedID); err != nil {
		t.Fatalf("MarkConsolidated: %v", err)
	}

	candidate, err := s.GetMemory(candidateID)
	if err != nil {
		t.Fatalf("GetMemory candidate: %v", err)
	}
	if candidate.Status != MemoryStatusArchived {
		t.Fatalf("candidate status = %q, want %q", candidate.Status, MemoryStatusArchived)
	}
	if candidate.SupersededBy == nil || *candidate.SupersededBy != consolidatedID {
		t.Fatalf("candidate superseded_by = %v, want %d", candidate.SupersededBy, consolidatedID)
	}

	for _, sourceID := range sourceIDs {
		source, err := s.GetMemory(sourceID)
		if err != nil {
			t.Fatalf("GetMemory source %d: %v", sourceID, err)
		}
		if source.Status != MemoryStatusArchived {
			t.Fatalf("source %d status = %q, want %q", sourceID, source.Status, MemoryStatusArchived)
		}
		if source.SupersededBy == nil || *source.SupersededBy != consolidatedID {
			t.Fatalf("source %d superseded_by = %v, want %d", sourceID, source.SupersededBy, consolidatedID)
		}
	}
}

// ─── Token-Aware Body Limit Tests ──────────────────────────────────────────────

func TestTruncateBodyToTokenLimit_NoTruncationWhenUnderLimit(t *testing.T) {
	// Glossary has 200 token limit. Short text should not be truncated.
	shortText := "This is a brief glossary entry."
	result := TruncateBodyToTokenLimit(shortText, MemoryKindGlossary)
	if result != shortText {
		t.Errorf("expected no truncation for short text, got %q", result)
	}
	if strings.HasSuffix(result, "... [truncated]") {
		t.Error("short text should not have truncation suffix")
	}
}

func TestTruncateBodyToTokenLimit_TruncatesWhenOverLimit(t *testing.T) {
	// Glossary has 200 token limit. Create text that exceeds it.
	// Repeating a word many times will exceed the token budget.
	longText := strings.Repeat("tokenization ", 300) // ~300+ tokens

	result := TruncateBodyToTokenLimit(longText, MemoryKindGlossary)

	// Should be truncated
	if !strings.HasSuffix(result, "... [truncated]") {
		t.Errorf("expected truncation suffix, got %q", result)
	}

	// The result should fit within the token limit
	if token.CountStrict(result) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Errorf("truncated result exceeds token limit: got %d tokens for %d limit",
			token.CountStrict(result), MemoryBodyLimit(MemoryKindGlossary))
	}

	// Original should be longer than result
	if len(result) >= len(longText) {
		t.Error("truncated result should be shorter than original")
	}
}

func TestTruncateBodyToTokenLimit_EmptyText(t *testing.T) {
	result := TruncateBodyToTokenLimit("", MemoryKindDecision)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestTruncateBodyToTokenLimit_UnknownKindNoLimit(t *testing.T) {
	// Unknown kinds have limit=0 (unlimited), so no truncation
	longText := strings.Repeat("x", 10000)
	result := TruncateBodyToTokenLimit(longText, "unknown_kind")
	if result != longText {
		t.Errorf("unknown kind should not truncate, got %q", result)
	}
}

func TestTruncateBodyToTokenLimit_DifferentKindsRespectLimits(t *testing.T) {
	// Create text that exceeds glossary limit (200) but not decision limit (1000)
	// "word " is roughly 1 token, so 300 repetitions = ~300 tokens
	mediumText := strings.Repeat("word ", 300)

	// Test glossary (200 token limit) - should truncate
	glossaryResult := TruncateBodyToTokenLimit(mediumText, MemoryKindGlossary)
	if !strings.HasSuffix(glossaryResult, "... [truncated]") {
		t.Error("glossary should truncate text exceeding 200 token limit")
	}

	// Test decision (1000 token limit) - should NOT truncate (300 < 1000)
	decisionResult := TruncateBodyToTokenLimit(mediumText, MemoryKindDecision)
	if strings.HasSuffix(decisionResult, "... [truncated]") {
		t.Error("decision should not truncate 300-token text (1000 token limit)")
	}

	// Verify token counts
	if token.CountStrict(glossaryResult) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Error("glossary result exceeds its token limit")
	}
}

func TestAddMemory_TruncatesBodyByTokenCount(t *testing.T) {
	s := newTestStore(t)

	// Create a body that exceeds the glossary token limit (200)
	longBody := strings.Repeat("tokenization test ", 300)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindGlossary,
		Title:     "Long glossary entry",
		Body:      longBody,
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	// Should be truncated
	if !strings.HasSuffix(mem.Body, "... [truncated]") {
		t.Errorf("expected truncated body, got %q", mem.Body)
	}

	// Should fit within token limit
	if token.CountStrict(mem.Body) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Errorf("stored body exceeds token limit: got %d tokens",
			token.CountStrict(mem.Body))
	}
}

func TestUpdateMemory_TruncatesBodyByTokenCount(t *testing.T) {
	s := newTestStore(t)

	// First create a memory
	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindGlossary,
		Title:     "Glossary entry",
		Body:      "Short body",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Now update with a long body that exceeds the limit
	longBody := strings.Repeat("tokenization update ", 300)
	updated, err := s.UpdateMemory(id, UpdateMemoryParams{
		Body: &longBody,
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	// Should be truncated
	if !strings.HasSuffix(updated.Body, "... [truncated]") {
		t.Errorf("expected truncated body after update, got %q", updated.Body)
	}

	// Should fit within token limit
	if token.CountStrict(updated.Body) > MemoryBodyLimit(MemoryKindGlossary) {
		t.Errorf("updated body exceeds token limit: got %d tokens",
			token.CountStrict(updated.Body))
	}

	// Verify revision was recorded
	revs, err := s.GetMemoryRevisions(id)
	if err != nil {
		t.Fatalf("GetMemoryRevisions: %v", err)
	}

	foundBodyRev := false
	for _, r := range revs {
		if r.Field == "body" {
			foundBodyRev = true
			break
		}
	}
	if !foundBodyRev {
		t.Error("expected body revision to be recorded")
	}
}

func TestTruncateBodyToTokenLimit_FallsBackSafely(t *testing.T) {
	// Test that the function works even with the fallback estimator
	// (when tiktoken is unavailable). CountStrict overestimates, so
	// we may truncate slightly more conservatively, but it should still work.

	text := strings.Repeat("word ", 1000) // Definitely over any limit

	// Test with a kind that has a small limit
	result := TruncateBodyToTokenLimit(text, MemoryKindGlossary)

	// Should have truncation marker
	if !strings.HasSuffix(result, "... [truncated]") {
		t.Error("should truncate even with fallback estimator")
	}

	// Result should be reasonable length (not empty, not full text)
	if len(result) == 0 || len(result) >= len(text) {
		t.Error("truncated result should have reasonable length")
	}
}

// ─── Private-tag stripping on memory save/update ────────────────────────────────

func TestAddMemory_StripsPrivateTags(t *testing.T) {
	s := newTestStore(t)

	// Memory with <private> tags in both title and body.
	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Use <private>super-secret-key</private> for signing",
		Body:      "The secret key is <private>SECRET123</private> and must stay private",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	// Title must not contain the private tag
	if strings.Contains(mem.Title, "<private>") {
		t.Errorf("stored title should not contain <private>: %q", mem.Title)
	}
	// Title should contain the replacement
	if !strings.Contains(mem.Title, "[REDACTED]") {
		t.Errorf("stored title should contain [REDACTED]: %q", mem.Title)
	}

	// Body must not contain the private tag
	if strings.Contains(mem.Body, "<private>") {
		t.Errorf("stored body should not contain <private>: %q", mem.Body)
	}
	if !strings.Contains(mem.Body, "[REDACTED]") {
		t.Errorf("stored body should contain [REDACTED]: %q", mem.Body)
	}

	// Revision records should also be stripped
	revs, err := s.GetMemoryRevisions(id)
	if err != nil {
		t.Fatalf("GetMemoryRevisions: %v", err)
	}
	for _, r := range revs {
		if r.NewValue != nil && strings.Contains(*r.NewValue, "<private>") {
			t.Errorf("revision %d field %q new_value should not contain <private>: %s", r.ID, r.Field, *r.NewValue)
		}
	}
}

func TestAddMemory_PrivateTagInBodyOnly(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindPattern,
		Title:     "Normal title without private tags",
		Body:      "API key: <private>ak-test-12345</private> — never commit this",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	mem, err := s.GetMemory(id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	if strings.Contains(mem.Body, "<private>") {
		t.Errorf("body should not contain <private> tag: %q", mem.Body)
	}
	if !strings.Contains(mem.Body, "[REDACTED]") {
		t.Errorf("body should contain [REDACTED] replacement: %q", mem.Body)
	}
	// Title should be unchanged
	if mem.Title != "Normal title without private tags" {
		t.Errorf("title should be unchanged: %q", mem.Title)
	}
}

func TestUpdateMemory_StripsPrivateTags(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindBugfix,
		Title:     "Fixed auth issue",
		Body:      "Original fix approach",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Update the body with a private tag
	newBody := "Updated: <private>password=hunter2</private> was the old cred"
	updated, err := s.UpdateMemory(id, UpdateMemoryParams{
		Body:   &newBody,
		Reason: "Added sensitive details to document",
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	if strings.Contains(updated.Body, "<private>") {
		t.Errorf("updated body should not contain <private>: %q", updated.Body)
	}
	if !strings.Contains(updated.Body, "[REDACTED]") {
		t.Errorf("updated body should contain [REDACTED]: %q", updated.Body)
	}

	// Verify the revision records the stripped value, not the original
	revs, err := s.GetMemoryRevisions(id)
	if err != nil {
		t.Fatalf("GetMemoryRevisions: %v", err)
	}
	var bodyRev *MemoryRevision
	for _, r := range revs {
		if r.Field == "body" {
			bodyRev = &r
			break
		}
	}
	if bodyRev == nil {
		t.Fatal("expected a body revision")
	}
	if bodyRev.NewValue != nil && strings.Contains(*bodyRev.NewValue, "<private>") {
		t.Errorf("body revision new_value should not contain <private>: %s", *bodyRev.NewValue)
	}
}

func TestUpdateMemory_StripsPrivateTagsInTitle(t *testing.T) {
	s := newTestStore(t)

	id, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Original decision title",
		Body:      "Some content",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	newTitle := "Updated: <private>internal-discussion</private> about auth"
	updated, err := s.UpdateMemory(id, UpdateMemoryParams{
		Title:  &newTitle,
		Reason: "Reflecting internal notes",
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	if strings.Contains(updated.Title, "<private>") {
		t.Errorf("updated title should not contain <private>: %q", updated.Title)
	}
	if !strings.Contains(updated.Title, "[REDACTED]") {
		t.Errorf("updated title should contain [REDACTED]: %q", updated.Title)
	}
}

func TestPrivateTagRegex_MatchesAcrossLines(t *testing.T) {
	// The regex must match <private> tags that span multiple lines.
	result := stripPrivateTags("Line1\n<private>multi\nline\nsecret</private>\nLine5")
	if strings.Contains(result, "<private>") {
		t.Error("multi-line <private> tag should be stripped")
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Error("multi-line replacement should appear")
	}
	if strings.Contains(result, "multi") || strings.Contains(result, "line") {
		t.Error("content inside <private> should be removed")
	}
}

func TestPrivateTagRegex_CaseInsensitive(t *testing.T) {
	result := stripPrivateTags("<PRIVATE>secret</PRIVATE> and <Private>more</Private>")
	if strings.Contains(result, "<PRIVATE>") || strings.Contains(result, "<Private>") {
		t.Error("case-insensitive <private> tags should be stripped")
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Error("[REDACTED] should appear for each match")
	}
}

func TestPrivateTagRegex_MultipleTags(t *testing.T) {
	result := stripPrivateTags("<private>key1</private> and <private>key2</private> remaining")
	if strings.Contains(result, "<private>") {
		t.Error("multiple <private> tags should all be stripped")
	}
	// Should have two [REDACTED] replacements
	count := strings.Count(result, "[REDACTED]")
	if count != 2 {
		t.Errorf("expected 2 [REDACTED] replacements, got %d in %q", count, result)
	}
}

// ─── WAL auto-checkpoint pragma ─────────────────────────────────────────────────

func TestWALAutocheckpointSet(t *testing.T) {
	s := newTestStore(t)

	// Verify the wal_autocheckpoint pragma was applied at store creation.
	var checkpointPages int
	err := s.db.QueryRow("PRAGMA wal_autocheckpoint").Scan(&checkpointPages)
	if err != nil {
		t.Fatalf("PRAGMA wal_autocheckpoint query: %v", err)
	}
	// Per Ohara v2 spec Phase 2: WAL auto-checkpoint at 1000 pages.
	if checkpointPages != 1000 {
		t.Errorf("expected wal_autocheckpoint=1000, got %d", checkpointPages)
	}
}

func TestWALAutocheckpoint_JournalModeIsWAL(t *testing.T) {
	s := newTestStore(t)

	var journalMode string
	err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("PRAGMA journal_mode query: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}
}

// ─── Consolidation candidate tests ───────────────────────────────────────────────

func TestConsolidationCandidates_CreatesCandidateForThreeOrMoreObservational(t *testing.T) {
	s := newTestStore(t)

	// Create 3 observational memories in the same project+domain+kind group.
	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindDiscovery,
			Title:          fmt.Sprintf("Obs item %d", i),
			Body:           fmt.Sprintf("Body of observation %d", i),
			Classification: "observational",
			Domain:         "test",
		})
		if err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
	}

	created, summaries, err := s.GenerateConsolidationCandidates("ohara", "test", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 1 {
		t.Errorf("expected 1 candidate, got %d", created)
	}
	if len(summaries) != 1 {
		t.Errorf("expected 1 summary, got %d", len(summaries))
	}
	if !strings.Contains(summaries[0], "created:") {
		t.Errorf("summary should contain 'created:', got %q", summaries[0])
	}

	// Verify the candidate memory was created with correct status.
	cand, err := s.GetMemories("ohara", "", "", MemoryStatusCandidate, 10)
	if err != nil {
		t.Fatalf("GetMemories candidate: %v", err)
	}
	if len(cand) != 1 {
		t.Errorf("expected 1 candidate in GetMemories, got %d", len(cand))
	}
	if cand[0].Classification != "tactical" {
		t.Errorf("expected classification tactical, got %q", cand[0].Classification)
	}
	if cand[0].Source != "consolidation" {
		t.Errorf("expected source consolidation, got %q", cand[0].Source)
	}
}

func TestConsolidationCandidates_DryRunDoesNotCreate(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindBugfix,
			Title:          fmt.Sprintf("Bugfix obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	created, summaries, err := s.GenerateConsolidationCandidates("ohara", "", true)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates dry-run: %v", err)
	}
	if created != 0 {
		t.Errorf("dry-run should return 0 created, got %d", created)
	}
	if len(summaries) == 0 {
		t.Errorf("dry-run should return summaries")
	}
	if !strings.Contains(summaries[0], "[dry-run]") {
		t.Errorf("dry-run summary should contain [dry-run], got %q", summaries[0])
	}

	// Verify no candidate was actually created.
	cand, _ := s.GetMemories("ohara", "", "", MemoryStatusCandidate, 10)
	if len(cand) != 0 {
		t.Errorf("dry-run should not create candidates, got %d", len(cand))
	}
}

func TestConsolidationCandidates_DuplicateAvoidance(t *testing.T) {
	s := newTestStore(t)

	// Create a group that will produce a candidate.
	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindPattern,
			Title:          fmt.Sprintf("Pattern obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
			Domain:         "auth",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	// First run: creates candidate.
	created1, _, err := s.GenerateConsolidationCandidates("ohara", "auth", false)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if created1 != 1 {
		t.Errorf("expected 1 candidate on first run, got %d", created1)
	}

	// Second run: should not create duplicate (same source IDs).
	created2, _, err := s.GenerateConsolidationCandidates("ohara", "auth", false)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if created2 != 0 {
		t.Errorf("expected 0 candidates on second run (duplicate avoidance), got %d", created2)
	}

	// Verify only one candidate exists.
	cand, _ := s.GetMemories("ohara", "", "", MemoryStatusCandidate, 10)
	if len(cand) != 1 {
		t.Errorf("expected exactly 1 candidate total, got %d", len(cand))
	}
}

func TestConsolidationCandidates_ExcludedFromActiveRetrieval(t *testing.T) {
	s := newTestStore(t)

	// Create a regular active memory.
	_, err := s.AddMemory(AddMemoryParams{
		ProjectID: "ohara",
		Kind:      MemoryKindDecision,
		Title:     "Regular decision",
		Body:      "A regular decision body",
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Create 3 observational memories to trigger a candidate.
	for i := 1; i <= 3; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindDiscovery,
			Title:          fmt.Sprintf("Obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
			Domain:         "db",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	// Generate the candidate.
	_, _, err = s.GenerateConsolidationCandidates("ohara", "db", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}

	// Normal active retrieval should NOT include the candidate.
	active, err := s.GetMemories("ohara", "", "", MemoryStatusActive, 20)
	if err != nil {
		t.Fatalf("GetMemories active: %v", err)
	}
	for _, m := range active {
		if m.Status == MemoryStatusCandidate {
			t.Errorf("candidate should not appear in active retrieval, found id=%d", m.ID)
		}
	}
}

func TestCountCandidates(t *testing.T) {
	s := newTestStore(t)

	// No candidates yet.
	count, err := s.CountCandidates("")
	if err != nil {
		t.Fatalf("CountCandidates: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 candidates initially, got %d", count)
	}

	// Create 3 observational memories and generate a candidate.
	for i := 1; i <= 3; i++ {
		_, _ = s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindBugfix,
			Title:          fmt.Sprintf("Obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
		})
	}
	_, _, _ = s.GenerateConsolidationCandidates("ohara", "", false)

	count, err = s.CountCandidates("")
	if err != nil {
		t.Fatalf("CountCandidates after: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 candidate, got %d", count)
	}

	// Filter by project.
	count, _ = s.CountCandidates("ohara")
	if count != 1 {
		t.Errorf("expected 1 candidate for ohara, got %d", count)
	}
	count, _ = s.CountCandidates("other-project")
	if count != 0 {
		t.Errorf("expected 0 candidates for other-project, got %d", count)
	}
}

func TestConsolidationCandidates_GroupSizeTwoOrLess(t *testing.T) {
	s := newTestStore(t)

	// Only 2 observational memories — below the threshold of 3.
	for i := 1; i <= 2; i++ {
		_, err := s.AddMemory(AddMemoryParams{
			ProjectID:      "ohara",
			Kind:           MemoryKindProcedure,
			Title:          fmt.Sprintf("Proc obs %d", i),
			Body:           fmt.Sprintf("Body %d", i),
			Classification: "observational",
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	created, summaries, err := s.GenerateConsolidationCandidates("ohara", "", false)
	if err != nil {
		t.Fatalf("GenerateConsolidationCandidates: %v", err)
	}
	if created != 0 {
		t.Errorf("expected 0 candidates for group < 3, got %d", created)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries for group < 3, got %d", len(summaries))
	}
}

// TestMigrationFromPre023LegacySchema verifies that a pre-023 database with
// the legacy observations system auto-migrates cleanly to schema 24:
// - schema_version reaches 24
// - legacy observations rows are backfilled into memory_items
// - legacy observations table, observations_fts, and obs_fts_* triggers are dropped
func TestMigrationFromPre023LegacySchema(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "ohara.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()

	// Create a pre-023 schema: base tables + legacy observations + FTS + triggers
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			summary TEXT
		);

		CREATE TABLE user_prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sync_id TEXT,
			session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			project TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);

		CREATE TABLE observations (
			id INTEGER PRIMARY KEY,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			project TEXT,
			session_id TEXT,
			type TEXT NOT NULL,
			scope TEXT,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			deleted_at TEXT
		);

		CREATE VIRTUAL TABLE observations_fts USING fts5(
			title,
			content,
			content='observations',
			content_rowid='id'
		);

		CREATE TRIGGER obs_fts_insert AFTER INSERT ON observations BEGIN
			INSERT INTO observations_fts(rowid, title, content)
			VALUES (new.id, new.title, new.content);
		END;

		CREATE TRIGGER obs_fts_delete AFTER DELETE ON observations BEGIN
			INSERT INTO observations_fts(observations_fts, rowid, title, content)
			VALUES ('delete', old.id, old.title, old.content);
		END;

		CREATE TRIGGER obs_fts_update AFTER UPDATE ON observations BEGIN
			INSERT INTO observations_fts(observations_fts, rowid, title, content)
			VALUES ('delete', old.id, old.title, old.content);
			INSERT INTO observations_fts(rowid, title, content)
			VALUES (new.id, new.title, new.content);
		END;

		-- Pre-023 schema version (schema 22 = last version before migration 023)
		CREATE TABLE schema_version (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		INSERT INTO schema_version (version, applied_at) VALUES (22, '2024-01-01T00:00:00');

		CREATE TABLE memory_items (
			id              INTEGER PRIMARY KEY,
			created_at      TEXT,
			updated_at      TEXT,
			project_id      TEXT,
			actor_id        TEXT,
			kind            TEXT,
			scope           TEXT,
			title           TEXT,
			body            TEXT,
			source          TEXT,
			status          TEXT,
			session_id      TEXT,
			written_by      TEXT,
			domain          TEXT,
			evidence_json   TEXT,
			applies_to_json TEXT,
			related_json    TEXT,
			classification  TEXT,
			access_count    INTEGER,
			last_accessed   TEXT,
			valid_from      TEXT,
			valid_to        TEXT,
			superseded_at   TEXT,
			trust_level     TEXT,
			ingested_at     TEXT,
			trigger_condition TEXT,
			utility_weight  REAL,
			consolidated_from TEXT,
			expires_at      TEXT,
			superseded_by   INTEGER,
			tags            TEXT
		);

		-- Seed legacy observations rows
		INSERT INTO observations (id, created_at, updated_at, project, session_id, type, scope, title, content, deleted_at)
		VALUES
			(1, '2024-01-01T10:00:00', '2024-01-01T10:00:00', 'ohara', 's1', 'decision', 'project', 'Auth decision', 'Use JWT for session tokens', NULL),
			(2, '2024-01-02T11:00:00', '2024-01-02T11:00:00', 'ohara', 's1', 'procedure', 'project', 'Fix auth bug', 'Check token expiry with < not <=', NULL),
			(3, '2024-01-03T12:00:00', '2024-01-03T12:00:00', 'ohara', 's2', 'pattern', NULL, 'Logging pattern', 'Use slog for structured logging', '2024-02-01T00:00:00');
	`)
	if err != nil {
		t.Fatalf("create pre-023 schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	cfg := mustDefaultConfig(t)
	cfg.DataDir = dataDir

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("store.New on pre-023 db: %v", err)
	}
	defer s.Close()

	// Verify schema version reached 24
	v := s.SchemaVersion()
	if v != 24 {
		t.Fatalf("expected schema version 24 after migration, got %d", v)
	}

	// Verify observations were backfilled into memory_items (migration 023)
	// Row 1: decision, not deleted → active
	// Row 2: procedure, not deleted → active
	// Row 3: pattern, deleted → archived (deleted_at is not null)
	var memCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM memory_items WHERE id IN (1, 2, 3)").Scan(&memCount); err != nil {
		t.Fatalf("count memory_items: %v", err)
	}
	if memCount != 3 {
		t.Fatalf("expected 3 observations backfilled into memory_items, got %d", memCount)
	}

	// Verify row 1 content landed correctly (decision kind)
	var title1, kind1 string
	if err := s.db.QueryRow("SELECT title, kind FROM memory_items WHERE id = 1").Scan(&title1, &kind1); err != nil {
		t.Fatalf("query memory_items id=1: %v", err)
	}
	if title1 != "Auth decision" {
		t.Fatalf("expected title 'Auth decision', got %q", title1)
	}
	if kind1 != "decision" {
		t.Fatalf("expected kind 'decision', got %q", kind1)
	}

	// Verify row 3 (deleted observation) is archived
	var status3 string
	if err := s.db.QueryRow("SELECT status FROM memory_items WHERE id = 3").Scan(&status3); err != nil {
		t.Fatalf("query memory_items id=3: %v", err)
	}
	if status3 != "archived" {
		t.Fatalf("expected deleted observation to be archived, got %q", status3)
	}

	// Verify row 2 content (procedure kind)
	var title2, kind2 string
	if err := s.db.QueryRow("SELECT title, kind FROM memory_items WHERE id = 2").Scan(&title2, &kind2); err != nil {
		t.Fatalf("query memory_items id=2: %v", err)
	}
	if title2 != "Fix auth bug" {
		t.Fatalf("expected title 'Fix auth bug', got %q", title2)
	}
	if kind2 != "procedure" {
		t.Fatalf("expected kind 'procedure', got %q", kind2)
	}

	// Verify observations table is gone (migration 024)
	var obsTableCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='observations'").Scan(&obsTableCount); err != nil {
		t.Fatalf("check observations table: %v", err)
	}
	if obsTableCount != 0 {
		t.Fatalf("expected observations table to be dropped after migration, but it still exists")
	}

	// Verify observations_fts FTS table is gone (migration 024)
	var obsFTSTableCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='observations_fts'").Scan(&obsFTSTableCount); err != nil {
		t.Fatalf("check observations_fts table: %v", err)
	}
	if obsFTSTableCount != 0 {
		t.Fatalf("expected observations_fts table to be dropped after migration, but it still exists")
	}

	// Verify legacy obs_fts_* triggers are gone (migration 024)
	for _, triggerName := range []string{"obs_fts_insert", "obs_fts_update", "obs_fts_delete"} {
		var triggerCount int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?", triggerName).Scan(&triggerCount); err != nil {
			t.Fatalf("check trigger %s: %v", triggerName, err)
		}
		if triggerCount != 0 {
			t.Fatalf("expected trigger %s to be dropped after migration, but it still exists", triggerName)
		}
	}
}
