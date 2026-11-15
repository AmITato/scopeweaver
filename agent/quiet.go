package agent

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// quietShimScript is the generic wrapper. @DEFAULTS@ is replaced by one
// "flag value detect-prefix" line per quiet default when the shim is materialized.
//
//go:embed quiet/shim.sh
var quietShimScript string

// quietShimDefaults maps each known scanner to {flag, value, detect-prefix}
// triples. A triple's flags are appended only when the agent omitted them; the
// detect prefix handles equivalent spellings (-T4 for nmap -T2). Values are
// deliberately low: the counting proxy enforces the real per-host budget, these
// only stop a tool's own default burst from emptying that budget immediately.
var quietShimDefaults = map[string][][3]string{
	"nuclei":      {{"-rate-limit", "10", "-rate-limit"}, {"-concurrency", "5", "-concurrency"}},
	"ffuf":        {{"-rate", "10", "-rate"}, {"-p", "0.1", "-p"}},
	"feroxbuster": {{"--rate-limit", "10", "--rate-limit"}},
	"nmap":        {{"-T2", "", "-T"}, {"--max-rate", "100", "--max-rate"}},
	"masscan":     {{"--rate", "100", "--rate"}},
	"sqlmap":      {{"--delay", "0.5", "--delay"}, {"--threads=1", "", "--threads"}},
	"hydra":       {{"-t", "4", "-t"}},
}

var (
	quietShimOnce sync.Once
	quietShimDir  string
)

// quietShims materializes the shim directory once per process and returns it.
// An empty result means materialization failed; callers then inherit PATH.
func quietShims() string {
	quietShimOnce.Do(func() {
		dir, err := os.MkdirTemp("", "scopeweaver-shims-")
		if err != nil {
			return
		}
		for name, defaults := range quietShimDefaults {
			var table strings.Builder
			for _, d := range defaults {
				table.WriteString(d[0])
				table.WriteByte(' ')
				if d[1] == "" {
					table.WriteByte('-') // read(1) cannot preserve an empty field
				} else {
					table.WriteString(d[1])
				}
				table.WriteByte(' ')
				table.WriteString(d[2])
				table.WriteByte('\n')
			}
			body := strings.Replace(quietShimScript, "@DEFAULTS@", table.String(), 1)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
				return // leave quietShimDir empty so PATH is not pointed at a partial dir
			}
		}
		quietShimDir = dir
	})
	return quietShimDir
}

// quietPathEnv prepends the shim directory to PATH for Bash subprocesses in the
// op profile. The dir is first so a bare `nuclei` resolves to the shim; the shim
// finds the real binary further down and execs it. Nil for pentest or when the
// shims could not be written.
func quietPathEnv(profile string) []string {
	if normalizeProfile(profile) != ProfileOp {
		return nil
	}
	dir := quietShims()
	if dir == "" {
		return nil
	}
	path := dir
	if inherited := os.Getenv("PATH"); inherited != "" {
		path += string(os.PathListSeparator) + inherited
	}
	return []string{"PATH=" + path}
}
