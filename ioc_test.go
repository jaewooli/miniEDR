package miniedr

import (
	"testing"

	"github.com/jaewooli/miniedr/capturer"
)

func TestRuleIOCMatchProcess(t *testing.T) {
	rule := RuleIOCMatch(IOCConfig{
		ProcessNames: []string{"evil.exe"},
	})
	info := capturer.InfoData{
		Fields: map[string]interface{}{
			"proc.new": []capturer.ProcMeta{
				{PID: 42, Name: "evil.exe", Exe: "/tmp/evil.exe", Cmdline: "evil.exe --run"},
			},
		},
	}
	alerts := rule.Eval(info)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}

func TestRuleIOCMatchFile(t *testing.T) {
	rule := RuleIOCMatch(IOCConfig{
		FilePaths: []string{"/tmp/evil.txt"},
	})
	info := capturer.InfoData{
		Fields: map[string]interface{}{
			"file.events": []capturer.FileEvent{
				{Path: "/tmp/evil.txt", Type: capturer.FileCreated},
			},
		},
	}
	alerts := rule.Eval(info)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}

func TestRuleIOCMatchRemoteIP(t *testing.T) {
	rule := RuleIOCMatch(IOCConfig{
		RemoteIPs: []string{"10.0.0.8"},
	})
	info := capturer.InfoData{
		Fields: map[string]interface{}{
			"conn.new": []capturer.ConnID{
				{PID: 1, RIP: "10.0.0.8", RPort: 443},
			},
		},
	}
	alerts := rule.Eval(info)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}
