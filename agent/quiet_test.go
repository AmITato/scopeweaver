package agent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTool writes a standing executable that prints each argument on its own
// line and exits with FAKE_EXIT (default 0).
func fakeTool(t *testing.T, dir, name string) {
	t.Helper()
	body := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\nexit ${FAKE_EXIT:-0}\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func shimEnv(t *testing.T, fakeDir string) string {
	t.Helper()
	shims := quietShims()
	if shims == "" {
		t.Fatal("shims were not materialized")
	}
	t.Setenv("PATH", shims+string(os.PathListSeparator)+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return shims
}

func runShim(t *testing.T, name string, args ...string) ([]string, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out)), code
}

func countFlag(args []string, flag string) int {
	n := 0
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			n++
		}
	}
	return n
}

func TestQuietShimsAddFlagsOnlyWhenAbsent(t *testing.T) {
	fakeDir := t.TempDir()
	for name := range quietShimDefaults {
		fakeTool(t, fakeDir, name)
	}
	fakeTool(t, fakeDir, "othertool")
	shimEnv(t, fakeDir)

	t.Run("adds missing defaults", func(t *testing.T) {
		args, code := runShim(t, "nuclei", "target.example")
		if code != 0 || countFlag(args, "-rate-limit") != 1 || countFlag(args, "-concurrency") != 1 {
			t.Fatalf("nuclei defaults not added: %v (exit %d)", args, code)
		}
		if args[0] != "target.example" {
			t.Fatalf("caller arguments moved: %v", args)
		}
		if args[1] != "-rate-limit" || args[2] != "10" {
			t.Fatalf("unexpected flag order: %v", args)
		}
	})
	t.Run("keeps separate and equals forms", func(t *testing.T) {
		args, _ := runShim(t, "nuclei", "-rate-limit", "50", "target.example")
		if countFlag(args, "-rate-limit") != 1 {
			t.Fatalf("existing -rate-limit duplicated: %v", args)
		}
		args, _ = runShim(t, "nuclei", "--rate-limit=50", "target.example")
		if got := strings.Join(args, " "); got != "--rate-limit=50 target.example -concurrency 5" {
			t.Fatalf("unexpected equals-form rewrite: %v", args)
		}
	})
	t.Run("short and value-less flags", func(t *testing.T) {
		args, _ := runShim(t, "nmap", "-T4", "target.example")
		if countFlag(args, "-T2") != 0 {
			t.Fatalf("existing -T4 overridden: %v", args)
		}
		if countFlag(args, "--max-rate") != 1 {
			t.Fatalf("nmap max-rate default missing: %v", args)
		}
		args, _ = runShim(t, "nmap", "-T", "2", "target.example")
		if countFlag(args, "-T2") != 0 {
			t.Fatalf("existing -T 2 overridden: %v", args)
		}
		args, _ = runShim(t, "sqlmap", "--threads", "2", "-u", "http://target")
		if countFlag(args, "--threads") != 1 || countFlag(args, "--threads=1") != 0 || countFlag(args, "--delay") != 1 {
			t.Fatalf("unexpected sqlmap rewrite: %v", args)
		}
	})
	t.Run("preserves exit code", func(t *testing.T) {
		t.Setenv("FAKE_EXIT", "7")
		if _, code := runShim(t, "hydra"); code != 7 {
			t.Fatalf("exit code %d, want 7", code)
		}
	})
	t.Run("leaves other tools alone", func(t *testing.T) {
		t.Setenv("FAKE_EXIT", "0")
		args, code := runShim(t, "othertool", "a", "b")
		if code != 0 || strings.Join(args, " ") != "a b" {
			t.Fatalf("unrelated command rewritten: %v (exit %d)", args, code)
		}
	})
	t.Run("works through bash PATH lookup", func(t *testing.T) {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("bash unavailable")
		}
		// The Bash tool runs `bash -c`; prove $0 still resolves the shim there.
		out, err := exec.Command("bash", "-c", "feroxbuster target.example").Output()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(out)); got != "target.example\n--rate-limit\n10" {
			t.Fatalf("bash lookup bypassed the shim: %q", got)
		}
	})
}

func TestQuietPathEnvGatedOnProfile(t *testing.T) {
	if env := quietPathEnv(ProfilePentest); env != nil {
		t.Fatalf("pentest must not add shims: %v", env)
	}
	env := quietPathEnv(ProfileOp)
	if len(env) != 1 || !strings.HasPrefix(env[0], "PATH=") {
		t.Fatalf("op must prepend a shim PATH: %v", env)
	}
	path := strings.TrimPrefix(env[0], "PATH=")
	if !strings.HasPrefix(path, quietShims()+string(os.PathListSeparator)) {
		t.Fatalf("shim dir is not first on PATH: %q", path)
	}
}
