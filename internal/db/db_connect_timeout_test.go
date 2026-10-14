package db

import "testing"

func TestWithConnectTimeout(t *testing.T) {
	cases := map[string]string{
		"postgres://u:p@h:5432/d?sslmode=disable": "postgres://u:p@h:5432/d?connect_timeout=7&sslmode=disable",
		"postgres://u:p@h:5432/d":                 "postgres://u:p@h:5432/d?connect_timeout=7",
		"host=h dbname=d":                         "host=h dbname=d connect_timeout=7",
		"postgres://u@h/d?connect_timeout=3":      "postgres://u@h/d?connect_timeout=3",
	}
	for in, want := range cases {
		if got := withConnectTimeout(in, 7); got != want {
			t.Errorf("withConnectTimeout(%q) = %q, want %q", in, got, want)
		}
	}
}
