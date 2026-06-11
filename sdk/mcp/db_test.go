package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDBServer returns a server wired up with an in-memory SQLite database
// pre-populated with a small test table.
func newTestDBServer(t *testing.T) *server {
	t.Helper()
	srv := newTestServer()

	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	_, err = db.ExecContext(ctx,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO users VALUES (1,'Alice',30),(2,'Bob',25)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	srv.db = &dbBundle{sqliteDB: db}
	return srv
}

// ── SQLite tests ──────────────────────────────────────────────────────────────

func TestSQLiteQuery(t *testing.T) {
	srv := newTestDBServer(t)
	res, err := srv.toolSQLiteQuery(context.Background(), map[string]string{
		"query": "SELECT id, name FROM users ORDER BY id",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned isError: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "Alice") || !strings.Contains(text, "Bob") {
		t.Errorf("expected both rows in output, got: %s", text)
	}
	if !strings.Contains(text, "2 row(s)") {
		t.Errorf("expected row count in output, got: %s", text)
	}
}

func TestSQLiteQueryMissingArg(t *testing.T) {
	srv := newTestDBServer(t)
	_, err := srv.toolSQLiteQuery(context.Background(), map[string]string{})
	if err == nil {
		t.Error("expected error for missing query arg")
	}
}

func TestSQLiteExecute(t *testing.T) {
	srv := newTestDBServer(t)
	res, err := srv.toolSQLiteExecute(context.Background(), map[string]string{
		"statement": "INSERT INTO users VALUES (3,'Carol',35)",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned isError: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "1 row(s) affected") {
		t.Errorf("expected 1 row affected, got: %s", res.Content[0].Text)
	}
}

func TestSQLiteExecuteMissingArg(t *testing.T) {
	srv := newTestDBServer(t)
	_, err := srv.toolSQLiteExecute(context.Background(), map[string]string{})
	if err == nil {
		t.Error("expected error for missing statement arg")
	}
}

func TestSQLiteSchema(t *testing.T) {
	srv := newTestDBServer(t)
	res, err := srv.toolSQLiteSchema(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned isError: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "CREATE TABLE users") {
		t.Errorf("expected CREATE TABLE in output, got: %s", res.Content[0].Text)
	}
}

func TestSQLiteSchemaSpecificTable(t *testing.T) {
	srv := newTestDBServer(t)
	res, err := srv.toolSQLiteSchema(context.Background(), map[string]string{"table": "users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "users") {
		t.Errorf("expected table name in output, got: %s", res.Content[0].Text)
	}
}

func TestSQLiteSchemaUnknownTable(t *testing.T) {
	srv := newTestDBServer(t)
	res, err := srv.toolSQLiteSchema(context.Background(), map[string]string{"table": "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "No table") {
		t.Errorf("expected not-found message, got: %s", res.Content[0].Text)
	}
}

func TestSQLiteNotConfigured(t *testing.T) {
	srv := newTestServer()
	_, err := srv.toolSQLiteQuery(context.Background(), map[string]string{"query": "SELECT 1"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected not-configured error, got: %v", err)
	}
}

func TestPGNotConfigured(t *testing.T) {
	srv := newTestServer()
	_, err := srv.toolPGQuery(context.Background(), map[string]string{"query": "SELECT 1"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected not-configured error, got: %v", err)
	}
}

func TestMongoNotConfigured(t *testing.T) {
	srv := newTestServer()
	_, err := srv.toolMongoFind(context.Background(), map[string]string{"collection": "users"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected not-configured error, got: %v", err)
	}
}

// ── Tool list includes DB tools ───────────────────────────────────────────────

func TestToolsListIncludesDBTools(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`99`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	})
	resp := runMCPRequest(t, srv, req)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var result mcpToolsListResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{
		"sqlite_query", "sqlite_execute", "sqlite_schema",
		"pg_query", "pg_execute", "pg_schema",
		"mongo_find", "mongo_insert_one", "mongo_update", "mongo_delete",
		"mongo_aggregate", "mongo_collections", "mongo_generate_mongoose",
	}
	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("expected tool %q in list", w)
		}
	}
}
