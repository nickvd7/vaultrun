package db

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditFilterWhere(t *testing.T) {
	sid := uuid.New()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	cases := []struct {
		name   string
		f      AuditFilter
		where  string
		nargs  int
		argsAt map[int]interface{}
	}{
		{"empty", AuditFilter{}, "", 0, nil},
		{"actor scope", AuditFilter{Actor: "k1"}, " WHERE actor = $1", 1, map[int]interface{}{0: "k1"}},
		{"session and actor", AuditFilter{SessionID: &sid, Actor: "k1"}, " WHERE session_id = $1 AND actor = $2", 2, map[int]interface{}{0: sid, 1: "k1"}},
		{"prefix is escaped", AuditFilter{ActionPrefix: `a_b%`}, ` WHERE action LIKE $1 ESCAPE '\'`, 1, map[int]interface{}{0: `a\_b\%%`}},
		{"everything", AuditFilter{Actor: "k1", ActionPrefix: "apikey.", From: &from, To: &to},
			` WHERE actor = $1 AND action LIKE $2 ESCAPE '\' AND timestamp >= $3 AND timestamp < $4`, 4, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args := tc.f.where()
			if where != tc.where {
				t.Fatalf("where = %q, want %q", where, tc.where)
			}
			if len(args) != tc.nargs {
				t.Fatalf("len(args) = %d, want %d", len(args), tc.nargs)
			}
			for i, want := range tc.argsAt {
				if !reflect.DeepEqual(args[i], want) {
					t.Fatalf("args[%d] = %#v, want %#v", i, args[i], want)
				}
			}
		})
	}
}
