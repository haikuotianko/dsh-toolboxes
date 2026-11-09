package runtimes

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	snapshot := Snapshot{
		CheckedAt: time.Now().Truncate(time.Second),
		Host:      "windows-amd64",
		Entries: []Entry{
			{ID: "python", Kind: "interpreter", Command: "python", Version: "3.12.4", Found: true, Ext: "py"},
			{ID: "rustc", Kind: "compiler", Command: "rustc", Version: "1.83.0", Found: true},
		},
	}
	if err := Save(root, snapshot); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(root)
	if err != nil || loaded == nil {
		t.Fatalf("load: %v %v", loaded, err)
	}
	if len(loaded.Entries) != 2 || loaded.Entries[0].Version != "3.12.4" || loaded.Host != "windows-amd64" {
		t.Fatalf("round trip lost data: %+v", loaded)
	}
}

func TestGetReusesFreshCache(t *testing.T) {
	root := t.TempDir()
	// A hand-written snapshot with an impossible entry: if Get probed, that
	// entry would disappear.
	snapshot := Snapshot{
		CheckedAt: time.Now(),
		Host:      "test",
		Entries:   []Entry{{ID: "python", Command: "python", Version: "9.9.9", Found: true, Ext: "py"}},
	}
	if err := Save(root, snapshot); err != nil {
		t.Fatal(err)
	}
	got, cached, err := Get(context.Background(), root, time.Hour, false, false)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !cached || len(got.Entries) != 1 || got.Entries[0].Version != "9.9.9" {
		t.Fatalf("expected the cached snapshot, got cached=%v %+v", cached, got)
	}
}

func TestGetReprobesWhenStale(t *testing.T) {
	root := t.TempDir()
	stale := Snapshot{
		CheckedAt: time.Now().Add(-48 * time.Hour),
		Entries:   []Entry{{ID: "python", Command: "python", Version: "9.9.9", Found: true}},
	}
	if err := Save(root, stale); err != nil {
		t.Fatal(err)
	}
	got, cached, err := Get(context.Background(), root, time.Hour, false, false)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cached {
		t.Fatal("a stale snapshot must be re-probed")
	}
	for _, entry := range got.Entries {
		if entry.Version == "9.9.9" {
			t.Fatal("stale entry survived the re-probe")
		}
	}
}

func TestCustomEntrySurvivesRefresh(t *testing.T) {
	root := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Skip("no executable path")
	}
	snapshot, err := AddCustom(context.Background(), root, "probe-binary", "Test Binary", self, "txt", "", false)
	if err != nil {
		t.Fatalf("add custom: %v", err)
	}
	found := false
	for _, entry := range snapshot.Entries {
		if entry.ID == "probe-binary" && entry.Source == "custom" && entry.Found {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom entry missing after add: %+v", snapshot.Entries)
	}

	refreshed, err := Refresh(context.Background(), root, false)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	found = false
	for _, entry := range refreshed.Entries {
		if entry.ID == "probe-binary" {
			found = true
		}
	}
	if !found {
		t.Fatal("refresh dropped the user's custom runtime")
	}

	if _, err := RemoveCustom(root, "probe-binary"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after, _ := Load(root)
	for _, entry := range after.Entries {
		if entry.ID == "probe-binary" {
			t.Fatal("custom entry survived removal")
		}
	}
}

func TestAddCustomValidatesInput(t *testing.T) {
	root := t.TempDir()
	if _, err := AddCustom(context.Background(), root, "bad id!", "x", "x", "", "", false); err == nil {
		t.Fatal("invalid id accepted")
	}
	if _, err := AddCustom(context.Background(), root, "rust", "Rust", "   ", "", "", false); err == nil {
		t.Fatal("empty command accepted")
	}
}

func TestInterpreterForUsesSnapshot(t *testing.T) {
	root := t.TempDir()
	snapshot := Snapshot{CheckedAt: time.Now(), Entries: []Entry{
		{ID: "python", Command: "python", Path: "/opt/python/bin/python3", Version: "3.12.4", Found: true, Ext: "py"},
	}}
	if err := Save(root, snapshot); err != nil {
		t.Fatal(err)
	}
	if got := InterpreterFor(root, "python"); got != "/opt/python/bin/python3" {
		t.Fatalf("interpreter: %q", got)
	}
	if got := InterpreterFor(root, "cobol"); got != "" {
		t.Fatalf("unknown language should resolve to empty, got %q", got)
	}
}

func TestMajorMinor(t *testing.T) {
	cases := map[string][2]int{
		"5.1.26100.7019": {5, 1},
		"7.4.6":          {7, 4},
		"3.12.14":        {3, 12},
		"24":             {24, 0},
		"":               {0, 0},
	}
	for version, want := range cases {
		if got := majorOf(version); got != want[0] {
			t.Errorf("majorOf(%q)=%d want %d", version, got, want[0])
		}
		if got := minorOf(version); got != want[1] {
			t.Errorf("minorOf(%q)=%d want %d", version, got, want[1])
		}
	}
}

func TestNotesWarnAboutOldPowerShell(t *testing.T) {
	old := notes(Entry{ID: "powershell", Version: "5.1.26100.7019"})
	if old == "" || !contains(old, "5.1") {
		t.Fatalf("5.1 note missing: %q", old)
	}
	modern := notes(Entry{ID: "pwsh", Version: "7.4.6"})
	if !contains(modern, "7") {
		t.Fatalf("pwsh note: %q", modern)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
