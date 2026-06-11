// Database bundle — SQLite, PostgreSQL, and MongoDB clients for MCP tools.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// dbBundle holds optional database clients. Fields are nil when the
// corresponding datasource is not configured.
type dbBundle struct {
	sqliteDB *sql.DB
	pgDB     *sql.DB
	mongoDB  mongoDBHandle // interface; nil when Mongo not configured
}

// mongoDBHandle is a thin interface so the real mongo.Database and a fake
// can both satisfy it in tests.
type mongoDBHandle interface {
	mongoHandle() // marker
}

// initDBClients opens database connections based on environment variables.
// It is safe to call even when no env vars are set — in that case srv.db
// remains nil and all DB tools return a "not configured" error.
func initDBClients(ctx context.Context, srv *server) error {
	bundle := &dbBundle{}
	anySet := false

	if path := os.Getenv("MCP_SQLITE_PATH"); path != "" {
		anySet = true
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return fmt.Errorf("sqlite open %q: %w", path, err)
		}
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			return fmt.Errorf("sqlite ping %q: %w", path, err)
		}
		db.SetMaxOpenConns(1) // SQLite is single-writer
		bundle.sqliteDB = db
	}

	if dsn := os.Getenv("MCP_PG_DSN"); dsn != "" {
		anySet = true
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return fmt.Errorf("postgres open: %w", err)
		}
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			return fmt.Errorf("postgres ping: %w", err)
		}
		bundle.pgDB = db
	}

	if uri := os.Getenv("MCP_MONGO_URI"); uri != "" {
		anySet = true
		dbName := os.Getenv("MCP_MONGO_DB")
		if dbName == "" {
			dbName = "test"
		}
		mh, err := openMongoDB(ctx, uri, dbName)
		if err != nil {
			return fmt.Errorf("mongo connect: %w", err)
		}
		bundle.mongoDB = mh
	}

	if anySet {
		srv.db = bundle
	}
	return nil
}

// formatSQLRows renders *sql.Rows as a plain-text table and returns it.
func formatSQLRows(rows *sql.Rows) (string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "columns: %s\n", strings.Join(cols, " | "))
	sb.WriteString(strings.Repeat("-", 40) + "\n")

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	count := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = fmt.Sprintf("%v", v)
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteByte('\n')
		count++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	fmt.Fprintf(&sb, "(%d row(s))\n", count)
	return sb.String(), nil
}
