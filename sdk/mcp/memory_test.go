package main

import "testing"

func TestMemoryPathForKey(t *testing.T) {
	got, err := memoryPathForKey("prefs")
	if err != nil || got != ".vaultrun/memory/prefs" {
		t.Fatalf("got %q %v", got, err)
	}
	got, err = memoryPathForKey("team/notes")
	if err != nil || got != ".vaultrun/memory/team/notes" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "../x", "/abs", "a..b/../c", "bad key", "ends/", "team//notes", "team/./notes", "./x"} {
		if _, err := memoryPathForKey(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestMemoryKeyFromPath(t *testing.T) {
	k, ok := memoryKeyFromPath(".vaultrun/memory/prefs")
	if !ok || k != "prefs" {
		t.Fatalf("got %q %v", k, ok)
	}
	k, ok = memoryKeyFromPath("/.vaultrun/memory/team/notes")
	if !ok || k != "team/notes" {
		t.Fatalf("got %q %v", k, ok)
	}
	if _, ok := memoryKeyFromPath("/workspace/other"); ok {
		t.Fatal("should not match")
	}
	if _, ok := memoryKeyFromPath(".vaultrun/memory/team/./notes"); ok {
		t.Fatal("aliased path must not list")
	}
}
