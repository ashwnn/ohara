package store

const (
	schemaVersionTable = `
		CREATE TABLE IF NOT EXISTS schema_version (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now'))
		)`

	coreSchema = `
			CREATE TABLE IF NOT EXISTS sessions (
				id         TEXT PRIMARY KEY,
			project    TEXT NOT NULL,
			directory  TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at   TEXT,
			summary    TEXT
		);

			CREATE TABLE IF NOT EXISTS user_prompts (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				sync_id    TEXT,
				session_id TEXT    NOT NULL,
			content    TEXT    NOT NULL,
			project    TEXT,
			created_at TEXT    NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);

		CREATE INDEX IF NOT EXISTS idx_prompts_session ON user_prompts(session_id);
		CREATE INDEX IF NOT EXISTS idx_prompts_project ON user_prompts(project);
		CREATE INDEX IF NOT EXISTS idx_prompts_created ON user_prompts(created_at DESC);

		CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
			content,
			project,
			content='user_prompts',
			content_rowid='id'
		);

			CREATE TABLE IF NOT EXISTS sync_chunks (
				chunk_id    TEXT PRIMARY KEY,
				imported_at TEXT NOT NULL DEFAULT (datetime('now'))
			);

			CREATE TABLE IF NOT EXISTS sync_state (
				target_key           TEXT PRIMARY KEY,
				lifecycle            TEXT NOT NULL DEFAULT 'idle',
				last_enqueued_seq    INTEGER NOT NULL DEFAULT 0,
				last_acked_seq       INTEGER NOT NULL DEFAULT 0,
				last_pulled_seq      INTEGER NOT NULL DEFAULT 0,
				consecutive_failures INTEGER NOT NULL DEFAULT 0,
				backoff_until        TEXT,
				lease_owner          TEXT,
				lease_until          TEXT,
				last_error           TEXT,
				updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
			);

			CREATE TABLE IF NOT EXISTS sync_mutations (
				seq         INTEGER PRIMARY KEY AUTOINCREMENT,
				target_key  TEXT NOT NULL,
				entity      TEXT NOT NULL,
				entity_key  TEXT NOT NULL,
				op          TEXT NOT NULL,
				payload     TEXT NOT NULL,
				source      TEXT NOT NULL DEFAULT 'local',
				occurred_at TEXT NOT NULL DEFAULT (datetime('now')),
				acked_at    TEXT,
				FOREIGN KEY (target_key) REFERENCES sync_state(target_key)
			);
		`

	syncMutationsIndices1 = `
		CREATE INDEX IF NOT EXISTS idx_prompts_sync_id ON user_prompts(sync_id);
		CREATE INDEX IF NOT EXISTS idx_sync_mutations_target_seq ON sync_mutations(target_key, seq);
		CREATE INDEX IF NOT EXISTS idx_sync_mutations_pending ON sync_mutations(target_key, acked_at, seq);
	`

	syncMutationsIndices2 = `
		CREATE TABLE IF NOT EXISTS sync_enrolled_projects (
			project     TEXT PRIMARY KEY,
			enrolled_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_sync_mutations_project ON sync_mutations(project);
	`

	promptsFTSTriggers = `
			CREATE TRIGGER prompt_fts_insert AFTER INSERT ON user_prompts BEGIN
				INSERT INTO prompts_fts(rowid, content, project)
				VALUES (new.id, new.content, new.project);
			END;

			CREATE TRIGGER prompt_fts_delete AFTER DELETE ON user_prompts BEGIN
				INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
				VALUES ('delete', old.id, old.content, old.project);
			END;

			CREATE TRIGGER prompt_fts_update AFTER UPDATE ON user_prompts BEGIN
				INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
				VALUES ('delete', old.id, old.content, old.project);
				INSERT INTO prompts_fts(rowid, content, project)
				VALUES (new.id, new.content, new.project);
			END;
		`

	memoryItemsSchema = `
		CREATE TABLE IF NOT EXISTS memory_items (
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
			superseded_by   INTEGER REFERENCES memory_items(id),
			expires_at      TEXT,
			ingested_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f','now')),
			written_by      TEXT NOT NULL DEFAULT 'agent'
		);

		CREATE INDEX IF NOT EXISTS idx_mem_project ON memory_items(project_id, status);
		CREATE INDEX IF NOT EXISTS idx_mem_kind ON memory_items(kind, status);
		CREATE INDEX IF NOT EXISTS idx_mem_scope ON memory_items(scope, status);
		CREATE INDEX IF NOT EXISTS idx_mem_updated ON memory_items(updated_at);

		CREATE TABLE IF NOT EXISTS memory_revisions (
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
	`

	memoryItemsFTSTriggers = `
			CREATE VIRTUAL TABLE IF NOT EXISTS memory_items_fts USING fts5(
				title,
				body,
				tags,
				content='memory_items',
				content_rowid='id',
				tokenize='porter unicode61'
			);

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
		`
)
