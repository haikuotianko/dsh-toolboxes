package runlog

import (
	"strings"
	"testing"
	"time"
)

func TestWriteAndFinishRoundTrip(t *testing.T) {
	root := t.TempDir()
	writer, err := Start(root, "ffmpeg", []string{"-i", "in.mp4"}, "/tmp", "agent")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := writer.Write([]byte("frame= 1 speed=0.9x\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.Finish("done", 0, "")

	record, err := Load(root, writer.ID())
	if err != nil || record == nil {
		t.Fatalf("load: %v %v", record, err)
	}
	if record.Phase != "done" || record.ExitCode != 0 || record.Origin != "agent" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.Bytes != int64(len("frame= 1 speed=0.9x\r")) {
		t.Fatalf("byte counter: got %d", record.Bytes)
	}

	text, partial, err := Tail(root, writer.ID(), 1<<20)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if partial || !strings.Contains(text, "speed=0.9x") {
		t.Fatalf("tail: partial=%v text=%q", partial, text)
	}
}

func TestTailKeepsWholeLines(t *testing.T) {
	root := t.TempDir()
	writer, err := Start(root, "rg", nil, "", "page")
	if err != nil {
		t.Fatal(err)
	}
	writer.Write([]byte("first line\nsecond line\nthird line\n"))
	writer.Finish("done", 0, "")

	text, partial, err := Tail(root, writer.ID(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if !partial {
		t.Fatal("expected a partial tail")
	}
	if strings.Contains(text, "first") {
		t.Fatalf("cut text leaked: %q", text)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatalf("torn line: %q", text)
	}
}

func TestStaleRunBecomesLost(t *testing.T) {
	stale := Record{Phase: "running", UpdatedAt: time.Now().Add(-2 * StaleAfter)}
	if got := stale.Normalize(); got.Phase != "lost" || got.Error == "" {
		t.Fatalf("stale run not marked lost: %+v", got)
	}
	fresh := Record{Phase: "running", UpdatedAt: time.Now()}
	if got := fresh.Normalize(); got.Phase != "running" {
		t.Fatalf("fresh run marked %q", got.Phase)
	}
}

func TestListIsNewestFirst(t *testing.T) {
	root := t.TempDir()
	first, _ := Start(root, "a", nil, "", "page")
	first.Finish("done", 0, "")
	second, _ := Start(root, "b", nil, "", "page")
	second.Finish("done", 0, "")

	records, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 2 || records[0].ID != second.ID() || records[1].ID != first.ID() {
		t.Fatalf("ordering: %+v", records)
	}
}

func TestCancelFinishedRunFails(t *testing.T) {
	root := t.TempDir()
	writer, _ := Start(root, "jq", nil, "", "page")
	writer.Finish("done", 0, "")
	if err := Cancel(root, writer.ID()); err == nil {
		t.Fatal("cancelling a finished run should fail")
	}
	if err := Cancel(root, "nope"); err == nil {
		t.Fatal("cancelling an unknown run should fail")
	}
}

func TestClearFinishedKeepsRunning(t *testing.T) {
	root := t.TempDir()
	done, _ := Start(root, "a", nil, "", "page")
	done.Finish("done", 0, "")
	live, _ := Start(root, "b", nil, "", "agent")
	defer live.Finish("cancelled", 1, "")

	removed, err := ClearFinished(root)
	if err != nil || removed != 1 {
		t.Fatalf("clear: removed=%d err=%v", removed, err)
	}
	records, _ := List(root)
	if len(records) != 1 || records[0].ID != live.ID() {
		t.Fatalf("running record dropped: %+v", records)
	}
}
