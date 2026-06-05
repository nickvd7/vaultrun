// SQLite MCP tool handlers.
package main

import (
	"context"
	"fmt"
	"strings"
)

func (s *server) sqliteOrErr() error {
	if s.db == nil || s.db.sqliteDB == nil {
		return fmt.Errorf("SQLite not configured — set MCP_SQLITE_PATH")
	}
	return nil
}

func (s *server) toolSQLiteQuery(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	if err := s.sqliteOrErr(); err != nil {
		return mcpToolResult{}, err
	}
	query := args["query"]
	if query == "" {
		return mcpToolResult{}, fmt.Errorf("query is required")
	}
	rows, err := s.db.sqliteDB.QueryContext(ctx, query)
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	defer rows.Close()
	out, err := formatSQLRows(rows)
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	return textResult(out), nil
}

func (s *server) toolSQLiteExecute(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	if err := s.sqliteOrErr(); err != nil {
		return mcpToolResult{}, err
	}
	statement := args["statement"]
	if statement == "" {
		return mcpToolResult{}, fmt.Errorf("statement is required")
	}
	res, err := s.db.sqliteDB.ExecContext(ctx, statement)
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	rowsAffected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	text := fmt.Sprintf("OK — %d row(s) affected, last_insert_id=%d", rowsAffected, lastID)
	return textResult(text), nil
}

func (s *server) toolSQLiteSchema(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	if err := s.sqliteOrErr(); err != nil {
		return mcpToolResult{}, err
	}

	table := args["table"]
	var query string
	var queryArgs []any
	if table != "" {
		query = `SELECT name, sql FROM sqlite_master WHERE type='table' AND name = ? ORDER BY name`
		queryArgs = []any{table}
	} else {
		query = `SELECT name, sql FROM sqlite_master WHERE type='table' ORDER BY name`
	}

	rows, err := s.db.sqliteDB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return mcpToolResult{}, err
	}
	defer rows.Close()

	var sb strings.Builder
	found := false
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			return mcpToolResult{}, err
		}
		fmt.Fprintf(&sb, "-- Table: %s\n%s;\n\n", name, ddl)
		found = true
	}
	if err := rows.Err(); err != nil {
		return mcpToolResult{}, err
	}
	if !found {
		if table != "" {
			return textResult(fmt.Sprintf("No table named %q found.", table)), nil
		}
		return textResult("No tables found in this database."), nil
	}
	return textResult(sb.String()), nil
}
