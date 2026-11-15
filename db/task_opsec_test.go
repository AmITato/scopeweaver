package db

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

// assertTaskJSON compares JSON values without depending on jsonb's key order.
func assertTaskJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want JSON %s: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch: got %s want %s", got, want)
	}
}

func TestTaskOpsecDefaultsForUnawareCallers(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTaskWithOptions("opsec defaults", "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(task.ID) }()

	got, err := d.GetTask(task.ID)
	if err != nil || got == nil {
		t.Fatalf("get task = %+v, %v", got, err)
	}
	if got.Mode != TaskModePentest || got.CoverStory != "" {
		t.Fatalf("unexpected text defaults: mode=%q cover=%q", got.Mode, got.CoverStory)
	}
	if string(got.HouseStyle) != "{}" || string(got.Stealth) != "{}" {
		t.Fatalf("unexpected JSON defaults: house_style=%s stealth=%s", got.HouseStyle, got.Stealth)
	}
	opsec := d.TaskOpsec(task.ID)
	if opsec.Mode != TaskModePentest || opsec.IsOp() || string(opsec.HouseStyle) != "{}" || string(opsec.Stealth) != "{}" {
		t.Fatalf("unexpected resolver defaults: %+v", opsec)
	}
}

func TestTaskOpsecLegacyRowDefaults(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	// An INSERT from a binary that predates the columns exercises the SQL defaults.
	var expID int64
	if err := d.QueryRow(`INSERT INTO explorations(description,goal) VALUES('legacy opsec','legacy') RETURNING id`).Scan(&expID); err != nil {
		t.Fatal(err)
	}
	var taskID int64
	if err := d.QueryRow(`INSERT INTO tasks(description,goal,exploration_id) VALUES('legacy opsec','legacy',$1) RETURNING id`, expID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(taskID) }()

	got, err := d.GetTask(taskID)
	if err != nil || got == nil {
		t.Fatalf("get legacy task = %+v, %v", got, err)
	}
	if got.Mode != TaskModePentest || got.CoverStory != "" || string(got.HouseStyle) != "{}" || string(got.Stealth) != "{}" {
		t.Fatalf("legacy row must read as pentest defaults: %+v", got)
	}
	opsec := d.TaskOpsec(taskID)
	if opsec.Mode != TaskModePentest || opsec.IsOp() {
		t.Fatalf("legacy resolver must fail open to pentest: %+v", opsec)
	}
	// Unknown and invalid ids also fail open.
	for _, id := range []int64{0, -1, 1 << 62} {
		if got := d.TaskOpsec(id); got.Mode != TaskModePentest || string(got.HouseStyle) != "{}" || string(got.Stealth) != "{}" {
			t.Fatalf("resolver for id %d = %+v", id, got)
		}
	}
}

func TestTaskOpsecCreateAndPatchRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTaskWithOptions("opsec create", "goal", TaskCreateOptions{
		Mode:       strPtr(" op "),
		CoverStory: strPtr("  contractor doing QuickBooks token integration  "),
		HouseStyle: json.RawMessage(`{"branch":"^(DEV|TS)-[0-9]+$"}`),
		Stealth:    json.RawMessage(`{"rps":1,"burst":5}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(task.ID) }()
	if task.Mode != TaskModeOp || task.CoverStory != "contractor doing QuickBooks token integration" {
		t.Fatalf("create did not normalize: %+v", task)
	}

	got, err := d.GetTask(task.ID)
	if err != nil || got == nil {
		t.Fatalf("get task = %+v, %v", got, err)
	}
	if got.Mode != TaskModeOp || got.CoverStory != "contractor doing QuickBooks token integration" {
		t.Fatalf("create round-trip mismatch: %+v", got)
	}
	assertTaskJSON(t, got.HouseStyle, `{"branch":"^(DEV|TS)-[0-9]+$"}`)
	assertTaskJSON(t, got.Stealth, `{"rps":1,"burst":5}`)

	// A partial patch changes only the supplied fields.
	updated, err := d.UpdateTask(task.ID, TaskPatch{CoverStory: strPtr("  support triage  ")})
	if err != nil || updated == nil {
		t.Fatalf("patch task = %+v, %v", updated, err)
	}
	if updated.CoverStory != "support triage" || updated.Mode != TaskModeOp {
		t.Fatalf("partial patch reset other fields: %+v", updated)
	}
	assertTaskJSON(t, updated.HouseStyle, `{"branch":"^(DEV|TS)-[0-9]+$"}`)
	assertTaskJSON(t, updated.Stealth, `{"rps":1,"burst":5}`)

	updated, err = d.UpdateTask(task.ID, TaskPatch{
		Mode: strPtr("pentest"), HouseStyle: json.RawMessage(`{}`), Stealth: json.RawMessage("null"),
	})
	if err != nil || updated == nil {
		t.Fatalf("reset task = %+v, %v", updated, err)
	}
	if updated.Mode != TaskModePentest || updated.CoverStory != "support triage" ||
		string(updated.HouseStyle) != "{}" || string(updated.Stealth) != "{}" {
		t.Fatalf("reset mismatch: %+v", updated)
	}
}

func TestTaskOpsecValidation(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	if _, err := d.CreateTaskWithOptions("bad mode", "goal", TaskCreateOptions{Mode: strPtr("scan")}); !errors.Is(err, ErrTaskModeInvalid) {
		t.Fatalf("invalid mode create error = %v", err)
	}
	if _, err := d.CreateTaskWithOptions("bad house style", "goal", TaskCreateOptions{HouseStyle: json.RawMessage(`[]`)}); !errors.Is(err, ErrTaskOpsecInvalid) {
		t.Fatalf("invalid house_style create error = %v", err)
	}
	if _, err := d.CreateTaskWithOptions("bad stealth", "goal", TaskCreateOptions{Stealth: json.RawMessage(`{`)}); !errors.Is(err, ErrTaskOpsecInvalid) {
		t.Fatalf("invalid stealth create error = %v", err)
	}

	task, err := d.CreateTaskWithOptions("patch validation", "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(task.ID) }()
	if _, err := d.UpdateTask(task.ID, TaskPatch{Mode: strPtr("bogus")}); !errors.Is(err, ErrTaskModeInvalid) {
		t.Fatalf("invalid mode patch error = %v", err)
	}
	if _, err := d.UpdateTask(task.ID, TaskPatch{Stealth: json.RawMessage(`"1rps"`)}); !errors.Is(err, ErrTaskOpsecInvalid) {
		t.Fatalf("invalid stealth patch error = %v", err)
	}
	if updated, err := d.UpdateTask(task.ID, TaskPatch{}); err != nil || updated == nil || updated.Mode != TaskModePentest {
		t.Fatalf("empty patch = %+v, %v", updated, err)
	}
	if missing, err := d.UpdateTask(1<<62, TaskPatch{Mode: strPtr(TaskModeOp)}); err != nil || missing != nil {
		t.Fatalf("missing task patch = %+v, %v", missing, err)
	}
}

func TestRestoreTaskStubDefaultsMissingOpsec(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTaskWithOptions("legacy archive", "goal", TaskCreateOptions{
		Mode:       strPtr(TaskModeOp),
		CoverStory: strPtr("current cover"),
		Stealth:    json.RawMessage(`{"rps":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(task.ID) }()

	// An archive written before the opsec columns has no keys for them; the
	// restore must fall back to the column defaults instead of NULL.
	now := time.Now().UTC()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	row := map[string]any{
		"id": task.ID, "name": "legacy archive", "description": "legacy", "goal": "goal",
		"status": "created", "paused": true, "llm_chain_revision": int64(0),
		"timeout_seconds": int64(0), "plan_heartbeat_seconds": int64(600),
		"coverage_enabled": true, "created_at": now, "updated_at": now,
	}
	if err := restoreTaskStub(tx, row, task.ID, 0); err != nil {
		t.Fatal(err)
	}
	var mode, cover string
	var house, stealth []byte
	if err := tx.QueryRow(`SELECT mode,cover_story,house_style,stealth FROM tasks WHERE id=$1`, task.ID).
		Scan(&mode, &cover, &house, &stealth); err != nil {
		t.Fatal(err)
	}
	if mode != TaskModePentest || cover != "" || string(house) != "{}" || string(stealth) != "{}" {
		t.Fatalf("legacy archive restore mismatch: mode=%q cover=%q house=%s stealth=%s", mode, cover, house, stealth)
	}
}
