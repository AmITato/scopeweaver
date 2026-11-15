package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/locale"
)

func TestOpsecBlockProfiles(t *testing.T) {
	if got := opsecBlock(ProfilePentest, locale.En); got != "" {
		t.Fatalf("pentest must have no opsec tail: %q", got)
	}
	if got := opsecBlock("", locale.En); got != "" {
		t.Fatalf("empty profile must default to pentest: %q", got)
	}
	op := opsecBlock(ProfileOp, locale.En)
	for _, want := range []string{"live-fire simulation", "residue ledger", "i<NNNN>", "rotate identity", "egress proxy"} {
		if !strings.Contains(op, want) {
			t.Fatalf("opsec tail missing %q: %q", want, op)
		}
	}
	bullets := 0
	for _, line := range strings.Split(op, "\n") {
		if strings.HasPrefix(line, "- ") {
			bullets++
		}
	}
	if bullets < 8 || bullets > 12 {
		t.Fatalf("opsec tail has %d bullets, want 8-12", bullets)
	}
	// Static text: no timestamps or task values, so the provider cache prefix stays valid.
	if again := opsecBlock("op", locale.En); again != op {
		t.Fatal("opsec tail is not deterministic")
	}
	ko := opsecBlock(ProfileOp, locale.Ko)
	if ko == op || !strings.Contains(ko, "잔류 원장") {
		t.Fatalf("opsec tail not translated: %q", ko)
	}
}

// TestOpsecBlockPlacement pins the append point in every role builder: after the
// constraint/goals/artifact block, before the output-language instruction.
func TestOpsecBlockPlacement(t *testing.T) {
	for _, tc := range []struct{ file, after, call string }{
		{"worker.go", "constraintBlock(ts,", "opsecBlock(w.profile()"},
		{"planner.go", "constraintBlock(ts,", "opsecBlock(p.profile()"},
		{"mainagent.go", "mainAgentSystemFor(", "opsecBlock("},
		{"chat.go", "chatSystemFor(", "opsecBlock("},
		{"goals.go", "goalsScopeTail)", "opsecBlock("},
	} {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(raw)
			after := strings.Index(src, tc.after)
			call := strings.Index(src, tc.call)
			lang := strings.Index(src, "outputLanguageInstruction")
			if after < 0 || call < 0 || lang < 0 {
				t.Fatalf("missing anchor: after=%d call=%d language=%d", after, call, lang)
			}
			if !(after < call && call < lang) {
				t.Fatalf("opsec tail out of order: after=%d call=%d language=%d", after, call, lang)
			}
		})
	}
}
