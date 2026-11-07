package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinLoads(t *testing.T) {
	cat, err := Builtin()
	if err != nil {
		t.Fatalf("builtin catalog: %v", err)
	}
	if len(cat.Tools) == 0 {
		t.Fatal("builtin catalog has no tools")
	}
	for _, tool := range cat.Tools {
		if tool.ID == "" {
			t.Errorf("tool with empty id: %+v", tool)
		}
		for _, source := range tool.Sources {
			if source.URL == "" {
				t.Errorf("%s has a source without url", tool.ID)
			}
		}
	}
}

func TestSourcesForFiltersPlatform(t *testing.T) {
	tool := Tool{Sources: []Source{
		{URL: "https://example.com/win.zip", OS: "windows", Arch: "amd64"},
		{URL: "https://example.com/mac.tar.gz", OS: "darwin", Arch: "arm64"},
		{URL: "https://example.com/any.tar.gz"},
	}}
	if got := tool.SourcesFor("windows", "amd64"); len(got) != 2 {
		t.Fatalf("windows/amd64: want 2 sources (win + unrestricted), got %d", len(got))
	}
	if got := tool.SourcesFor("linux", "amd64"); len(got) != 1 {
		t.Fatalf("linux/amd64: want the unrestricted source only, got %d", len(got))
	}
	if got := tool.SourcesFor("darwin", "arm64"); len(got) != 2 {
		t.Fatalf("darwin/arm64: want 2 sources, got %d", len(got))
	}
}

func TestLoadAppliesUserOverride(t *testing.T) {
	root := t.TempDir()
	user := `{"version":1,"tools":[{"id":"ffmpeg","title":"mine","sources":[{"url":"https://example.com/ff.zip"}]},{"id":"custom","sources":[{"url":"https://example.com/c.zip"}]}]}`
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ffmpeg, ok := cat.Find("ffmpeg")
	if !ok {
		t.Fatal("builtin ffmpeg disappeared")
	}
	if ffmpeg.Title != "mine" || len(ffmpeg.Sources) != 1 {
		t.Fatalf("user entry did not replace the builtin one: %+v", ffmpeg)
	}
	if _, ok := cat.Find("custom"); !ok {
		t.Fatal("user-added tool missing")
	}
}
