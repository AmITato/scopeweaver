package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/blend"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/locale"
	actool "github.com/Autumn-27/norma/tool"
)

func styleTools(t *testing.T, lang locale.Lang) (*ToolSet, *db.Task) {
	t.Helper()
	d := testDB(t)
	t.Cleanup(func() { d.Close() })
	tools, task := gh50Tools(t, d, lang)
	mode := db.TaskModeOp
	if _, err := d.UpdateTask(task.ID, db.TaskPatch{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	return tools, task
}

// callStyleJSON calls a style tool that is expected to succeed.
func callStyleJSON(t *testing.T, tool actool.CoreTool, input string) map[string]any {
	t.Helper()
	res, err := tool.Call(context.Background(), json.RawMessage(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Flatten())
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Flatten()), &out); err != nil {
		t.Fatalf("decode: %v; raw=%s", err, res.Flatten())
	}
	return out
}

func devBranchEntries() string {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := 0; i < 12; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"class":"branch","value":"DEV-`)
		b.WriteString(strconv.Itoa(38855 + i))
		b.WriteString(`","source":"github:acme/widgets/branches"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

func TestStyleRecordThenBlendName(t *testing.T) {
	tools, _ := styleTools(t, locale.En)

	out := callStyleJSON(t, tools.styleRecord(), devBranchEntries())
	if accepted, _ := out["accepted"].(float64); accepted != 12 {
		t.Fatalf("accepted=%v out=%v", out["accepted"], out)
	}

	get := callStyleJSON(t, tools.styleGet(), `{}`)
	if unlearned, _ := get["unlearned"].([]any); contains(unlearned, "branch") {
		t.Fatalf("branch must be learned: %v", get["unlearned"])
	}
	class := get["classes"].(map[string]any)["branch"].(map[string]any)
	if usable, _ := class["usable"].(bool); !usable {
		t.Fatalf("branch not usable: %v", class)
	}

	named := callStyleJSON(t, tools.blendName(), `{"class":"branch","purpose":"wire durable token refresh"}`)
	name, _ := named["name"].(string)
	if !strings.HasPrefix(name, "DEV-") {
		t.Fatalf("name %q does not use the observed ticket prefix (out=%v)", name, named)
	}
	if conforms, _ := named["conforms"].(bool); !conforms {
		t.Fatalf("generated name must conform: %v", named)
	}
	alts, _ := named["alternatives"].([]any)
	if len(alts) > 3 {
		t.Fatalf("too many alternatives: %v", alts)
	}
	for _, raw := range alts {
		alt, _ := raw.(string)
		if alt == name || !strings.HasPrefix(alt, "DEV-") {
			t.Fatalf("bad alternative %q (primary %q)", alt, name)
		}
	}
}

func contains(list []any, want string) bool {
	for _, raw := range list {
		if raw == want {
			return true
		}
	}
	return false
}

func TestBlendNameRefusesUnlearnedClass(t *testing.T) {
	tools, _ := styleTools(t, locale.En)
	res, err := tools.blendName().Call(context.Background(), json.RawMessage(`{"class":"branch","purpose":"probe"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected an unlearned refusal: %s", res.Flatten())
	}
	if !strings.Contains(res.Flatten(), "style_record") {
		t.Fatalf("refusal must point at style_record: %s", res.Flatten())
	}
}

func TestStyleClearResetsGrammar(t *testing.T) {
	tools, _ := styleTools(t, locale.En)
	callStyleJSON(t, tools.styleRecord(), devBranchEntries())
	callStyleJSON(t, tools.styleClear(), `{}`)

	get := callStyleJSON(t, tools.styleGet(), `{}`)
	if unlearned, _ := get["unlearned"].([]any); !contains(unlearned, "branch") {
		t.Fatalf("branch must be unlearned after clear: %v", get["unlearned"])
	}
	if res, _ := tools.blendName().Call(context.Background(), json.RawMessage(`{"class":"branch","purpose":"probe"}`), nil); !res.IsError {
		t.Fatalf("naming after clear must refuse: %s", res.Flatten())
	}
}

func TestBlendNameRejectsNameFailingStoredRegex(t *testing.T) {
	tools, task := styleTools(t, locale.En)
	// The validator demands seven digits, which no bounded number variant renders,
	// so every candidate is rejected and the tool must say the name failed.
	st := &blend.Style{Version: blend.StyleVersion, Classes: map[blend.Class]*blend.ClassStyle{
		blend.ClassBranch: {
			Samples: 12, Confidence: 0.95, Template: "{TICKET}-{N}", TicketKeys: []string{"DEV"},
			Separator: "-", Regex: "^ZZZ-[0-9]{7}$", Case: "upper",
		},
	}}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := tools.as.SetHouseStyle(task.ID, raw); err != nil {
		t.Fatal(err)
	}

	res, err := tools.blendName().Call(context.Background(), json.RawMessage(`{"class":"branch","purpose":"probe","seq":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		if !strings.Contains(res.Flatten(), "style_record") {
			t.Fatalf("rejection must point at style_record: %s", res.Flatten())
		}
		return
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Flatten()), &out); err != nil {
		t.Fatalf("decode: %v; raw=%s", err, res.Flatten())
	}
	if conforms, _ := out["conforms"].(bool); conforms {
		t.Fatalf("DEV-shaped name must not conform to ^ZZZ-: %v", out)
	}
	if !strings.Contains(out["note"].(string), "validator") {
		t.Fatalf("missing nonconformance note: %v", out)
	}
}

func TestBlendNameReportsNonconformingName(t *testing.T) {
	tools, task := styleTools(t, locale.En)
	// The validator is damaged so generation proceeds, but the upper ticket cannot
	// satisfy the learned lower-case rule: the name is returned marked nonconforming.
	st := &blend.Style{Version: blend.StyleVersion, Classes: map[blend.Class]*blend.ClassStyle{
		blend.ClassBranch: {
			Samples: 12, Confidence: 0.95, Template: "{TICKET}-{N}", TicketKeys: []string{"DEV"},
			Separator: "-", Regex: "(", Case: "lower",
		},
	}}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := tools.as.SetHouseStyle(task.ID, raw); err != nil {
		t.Fatal(err)
	}
	out := callStyleJSON(t, tools.blendName(), `{"class":"branch","purpose":"probe"}`)
	if conforms, _ := out["conforms"].(bool); conforms {
		t.Fatalf("upper DEV name cannot conform to lower case: %v", out)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "style_record") {
		t.Fatalf("note must offer the re-record route: %v", out)
	}
}

func TestStyleRecordRejectsUnknownClass(t *testing.T) {
	tools, _ := styleTools(t, locale.En)
	out := callStyleJSON(t, tools.styleRecord(), `{"class":"spaceship","value":"USS-1"}`)
	if _, ok := out["errors"]; !ok {
		t.Fatalf("expected a per-entry error: %v", out)
	}
}

func TestStyleToolsRequireTask(t *testing.T) {
	tools := NewToolSet(nil, "worker", locale.En)
	for _, tool := range []actool.CoreTool{tools.styleRecord(), tools.styleGet(), tools.blendName(), tools.styleClear()} {
		out, err := tool.Call(context.Background(), json.RawMessage(`{"class":"branch","purpose":"p"}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !out.IsError {
			t.Fatalf("%s: expected an error without task context", tool.Name())
		}
	}
}

// TestStyleHarvestGitHub drives style_harvest against a stub API.
func TestStyleHarvestGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/branches"):
			_, _ = w.Write([]byte(`[{"name":"DEV-101-Fix-Login"},{"name":"DEV-102-Add-Export"}]`))
		case strings.HasSuffix(r.URL.Path, "/actions/workflows"):
			_, _ = w.Write([]byte(`{"workflows":[{"name":"pr-security","path":".github/workflows/pr-security.yml"}]}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)

	tools, task := residueTools(t, locale.En)
	out := callReadJSON(t, tools.styleHarvest(), `{"repo":"acme/widgets"}`).(map[string]any)
	if total, _ := out["total"].(float64); total < 2 {
		t.Fatalf("no observations recorded: %v", out)
	}
	style := blend.FromJSON(tools.as.TaskHouseStyle(task.ID))
	if cs := style.Classes[blend.ClassBranch]; cs == nil || cs.Samples < 2 {
		t.Fatalf("branch class not learned: %+v", style.Classes)
	}
	name, err := style.Name(blend.ClassBranch, "Token refresh", 103)
	if err != nil {
		t.Fatalf("generated name: %v", err)
	}
	if !strings.HasPrefix(name, "DEV-") {
		t.Fatalf("generated name does not follow the ticket grammar: %q", name)
	}
}
