package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/locale"
)

func residueTools(t *testing.T, lang locale.Lang) (*ToolSet, *db.Task) {
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

func residueRows(t *testing.T, tools *ToolSet) []any {
	t.Helper()
	out := callReadJSON(t, tools.residueList(), `{"include_removed":true}`).(map[string]any)
	rows, _ := out["residue"].([]any)
	return rows
}

func TestResidueToolsRoundTrip(t *testing.T) {
	tools, task := residueTools(t, locale.En)
	ctx := context.Background()

	added := callReadJSON(t, tools.residueAdd(), `{"entries":[
		{"kind":"github_branch","target":"acme/widgets","locator":"refs/heads/DEV-1-probe"},
		{"kind":"iam_user","target":"acct","locator":"svc-probe","rank":"plausible"}]}`).(map[string]any)
	if got, _ := added["added"].([]any); len(got) != 2 {
		t.Fatalf("added=%v", added)
	}

	list := callReadJSON(t, tools.residueList(), `{}`).(map[string]any)
	if open, _ := list["open_obvious"].(float64); open != 1 {
		t.Fatalf("open_obvious=%v, want 1", list["open_obvious"])
	}

	var branchID int64
	for _, raw := range residueRows(t, tools) {
		row := raw.(map[string]any)
		if row["kind"] == "github_branch" {
			branchID = int64(row["id"].(float64))
		}
	}
	if branchID == 0 {
		t.Fatal("branch row missing")
	}

	if _, err := tools.residueRemove().Call(ctx, json.RawMessage(`{"id":`+strconv.FormatInt(branchID, 10)+`}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, blocked, err := tools.as.ResidueCompletionGate(task.ID); err != nil || blocked {
		t.Fatalf("removal must release the gate: blocked=%v err=%v", blocked, err)
	}
}

func TestResidueKeepAllReleasesGate(t *testing.T) {
	tools, task := residueTools(t, locale.En)
	callReadJSON(t, tools.residueAdd(), `{"kind":"host_marker","target":"h","locator":"C:/x"}`)
	if _, blocked, _ := tools.as.ResidueCompletionGate(task.ID); !blocked {
		t.Fatal("expected the gate to hold")
	}
	out := callReadJSON(t, tools.residueKeepAll(), `{}`).(map[string]any)
	if kept, _ := out["kept"].(float64); kept != 1 {
		t.Fatalf("kept=%v", out["kept"])
	}
	if _, blocked, _ := tools.as.ResidueCompletionGate(task.ID); blocked {
		t.Fatal("keep_all must release the gate")
	}
}

func TestResidueSetRank(t *testing.T) {
	tools, task := residueTools(t, locale.En)
	callReadJSON(t, tools.residueAdd(), `{"kind":"s3_object","target":"b","locator":"k"}`)
	rows := callReadJSON(t, tools.residueList(), `{}`).(map[string]any)["residue"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows=%v", rows)
	}
	id := int64(rows[0].(map[string]any)["id"].(float64))
	out := callReadJSON(t, tools.residueSet(), `{"id":`+strconv.FormatInt(id, 10)+`,"rank":"keep"}`).(map[string]any)
	if out["rank"] != "keep" {
		t.Fatalf("rank not updated: %v", out)
	}
	if _, blocked, _ := tools.as.ResidueCompletionGate(task.ID); blocked {
		t.Fatal("keep rank must not gate")
	}
}

func TestResidueToolsRequireTask(t *testing.T) {
	tools := NewToolSet(nil, "worker", locale.En)
	out, err := tools.residueList().Call(context.Background(), json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsError {
		t.Fatalf("expected an error without task context: %s", out.Flatten())
	}
}

func TestResidueAddRejectsUnknownRank(t *testing.T) {
	tools, _ := residueTools(t, locale.En)
	out := callReadJSON(t, tools.residueAdd(), `{"kind":"k","target":"t","locator":"l","rank":"urgent"}`).(map[string]any)
	if _, ok := out["errors"]; !ok {
		t.Fatalf("expected a per-entry error: %v", out)
	}
}
