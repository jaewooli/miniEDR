package miniedr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAlertFileResponderWritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alerts.jsonl")
	responder := NewAlertFileResponder(path, 1)

	if err := responder.Handle(Alert{ID: "1"}); err != nil {
		t.Fatalf("handle alert: %v", err)
	}
	if err := responder.Handle(Alert{ID: "2"}); err != nil {
		t.Fatalf("handle alert: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("alert file missing: %v", err)
	}
	matches, _ := filepath.Glob(path + ".*")
	if len(matches) == 0 {
		t.Fatalf("expected rotated alert file")
	}
}

func TestProcessKillerResponder(t *testing.T) {
	var got []int
	responder := &ProcessKillerResponder{
		KillFn: func(pid int) error {
			got = append(got, pid)
			return nil
		},
	}
	alert := Alert{
		Evidence: map[string]any{
			"pid": int32(42),
		},
	}
	if err := responder.Handle(alert); err != nil {
		t.Fatalf("handle alert: %v", err)
	}
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("unexpected pids: %+v", got)
	}
}
