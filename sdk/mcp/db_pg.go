// PostgreSQL MCP tool handlers.
package main

import (
	"context"
	"fmt"
	"strings"
)

func (s *server) pgOrErr() error {
	if s.db == nil || s.db.pgDB == nil {
		return fmt.Errorf("PostgreSQL not configured — set MCP_PG_DSN")
	}
	return nil
}

func (s *server) toolPGQuery(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	if err := s.pgOrErr(); err != nil {
		return mcpToolResult{}, err
	}
	query := args["query"]
	if query == "" {
		return mcpToolResult{}, fmt.Errorf("query is required")
	}
	rows, err := s.db.pgDB.QueryContext(ctx, query)
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

func (s *server) toolPGExecute(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	if err := s.pgOrErr(); err != nil {
		return mcpToolResult{}, err
	}
	statement := args["statement"]
	if statement == "" {
		return mcpToolResult{}, fmt.Errorf("statement is required")
	}
	res, err := s.db.pgDB.ExecContext(ctx, statement)
	if err != nil {
		return mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}, nil
	}
	rowsAffected, _ := res.RowsAffected()
	return textResult(fmt.Sprintf("OK — %d row(s) affected", rowsAffected)), nil
}

func (s *server) toolPGSchema(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	if err := s.pgOrErr(); err != nil {
		return mcpToolResult{}, err
	}

	schemaName := coalesce(args["schema"], "public")
	table := args["table"]

	var query string
	var queryArgs []any
	if table != "" {
		query = `
SELECT c.table_name,
       c.column_name,
       c.data_type,
       c.is_nullable,
       c.column_default
FROM   information_schema.columns c
WHERE  c.table_schema = $1
  AND  c.table_name   = $2
ORDER  BY c.table_name, c.ordinal_position`
		queryArgs = []any{schemaName, table}
	} else {
		query = `
SELECT c.table_name,
       c.column_name,
       c.data_type,
       c.is_nullable,
       c.column_default
FROM   information_schema.columns c
WHERE  c.table_schema = $1
ORDER  BY c.table_name, c.ordinal_position`
		queryArgs = []any{schemaName}
	}

	rows, err := s.db.pgDB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return mcpToolResult{}, err
	}
	defer rows.Close()

	type colInfo struct {
		column     string
		dataType   string
		isNullable string
		colDefault string
	}
	tableMap := make(map[string][]colInfo)
	tableOrder := []string{}
	seen := make(map[string]bool)

	for rows.Next() {
		var tbl, col, dt, nullable string
		var def *string
		if err := rows.Scan(&tbl, &col, &dt, &nullable, &def); err != nil {
			return mcpToolResult{}, err
		}
		defStr := ""
		if def != nil {
			defStr = *def
		}
		if !seen[tbl] {
			seen[tbl] = true
			tableOrder = append(tableOrder, tbl)
		}
		tableMap[tbl] = append(tableMap[tbl], colInfo{col, dt, nullable, defStr})
	}
	if err := rows.Err(); err != nil {
		return mcpToolResult{}, err
	}
	if len(tableOrder) == 0 {
		if table != "" {
			return textResult(fmt.Sprintf("No table %q in schema %q.", table, schemaName)), nil
		}
		return textResult(fmt.Sprintf("No tables found in schema %q.", schemaName)), nil
	}

	var sb strings.Builder
	for _, tbl := range tableOrder {
		fmt.Fprintf(&sb, "-- Table: %s.%s\n", schemaName, tbl)
		for _, c := range tableMap[tbl] {
			nullable := "NOT NULL"
			if c.isNullable == "YES" {
				nullable = "NULL"
			}
			if c.colDefault != "" {
				fmt.Fprintf(&sb, "  %-30s %s  %s  DEFAULT %s\n", c.column, c.dataType, nullable, c.colDefault)
			} else {
				fmt.Fprintf(&sb, "  %-30s %s  %s\n", c.column, c.dataType, nullable)
			}
		}
		sb.WriteByte('\n')
	}
	return textResult(sb.String()), nil
}
