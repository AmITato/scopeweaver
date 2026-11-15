package db

import (
	"testing"

	"github.com/Autumn-27/artex/internal/dbtest"
)

func residueTask(t *testing.T, d *DB, mode string) *Task {
	t.Helper()
	task, err := d.CreateTaskWithOptions("residue fixture", "goal", TaskCreateOptions{Mode: &mode})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	return task
}

func TestResidueAddListRemove(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := residueTask(t, d, TaskModeOp)

	id, err := d.AddResidue(task.ID, "github_branch", "acme/widgets", "refs/heads/DEV-1-probe", "contractor", ResidueRankObvious, true)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListResidue(task.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Locator != "refs/heads/DEV-1-probe" || rows[0].Rank != ResidueRankObvious {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if _, err := d.RemoveResidue(id); err != nil {
		t.Fatal(err)
	}
	open, err := d.ListResidue(task.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("removed row still open: %+v", open)
	}
	all, err := d.ListResidue(task.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].RemovedAt == nil {
		t.Fatalf("removed row missing from include_removed: %+v", all)
	}
}

func TestResidueValidation(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := residueTask(t, d, TaskModeOp)
	if _, err := d.AddResidue(task.ID, "", "t", "l", "", "", true); err == nil {
		t.Fatal("empty kind must fail")
	}
	if _, err := d.AddResidue(task.ID, "k", "t", "", "", "", true); err == nil {
		t.Fatal("empty locator must fail")
	}
	if _, err := d.AddResidue(task.ID, "k", "t", "l", "", "urgent", true); err == nil {
		t.Fatal("unknown rank must fail")
	}
	id, err := d.AddResidue(task.ID, "k", "t", "l", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	bad := "urgent"
	if _, err := d.SetResidue(id, ResiduePatch{Rank: &bad}); err == nil {
		t.Fatal("unknown patch rank must fail")
	}
}

func TestResidueGateOnlyBlocksObviousOnOp(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()

	pentest := residueTask(t, d, TaskModePentest)
	if _, err := d.AddResidue(pentest.ID, "github_branch", "t", "l", "", ResidueRankObvious, true); err != nil {
		t.Fatal(err)
	}
	if open, blocked, err := d.ResidueCompletionGate(pentest.ID); err != nil || blocked || open != 0 {
		t.Fatalf("pentest must not gate: open=%d blocked=%v err=%v", open, blocked, err)
	}

	op := residueTask(t, d, TaskModeOp)
	if _, blocked, _ := d.ResidueCompletionGate(op.ID); blocked {
		t.Fatal("empty ledger must not gate")
	}
	obvious, _ := d.AddResidue(op.ID, "github_branch", "t", "refs/heads/x", "", ResidueRankObvious, true)
	if _, err := d.AddResidue(op.ID, "s3_object", "t", "k", "", ResidueRankPlausible, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddResidue(op.ID, "iam_user", "t", "svc", "", ResidueRankObvious, false); err != nil {
		t.Fatal(err)
	}
	if open, blocked, err := d.ResidueCompletionGate(op.ID); err != nil || !blocked || open != 1 {
		t.Fatalf("obvious removable row must gate: open=%d blocked=%v err=%v", open, blocked, err)
	}
	if _, err := d.RemoveResidue(obvious); err != nil {
		t.Fatal(err)
	}
	if _, blocked, _ := d.ResidueCompletionGate(op.ID); blocked {
		t.Fatal("removed row must stop gating")
	}

	// keep_all downgrades the remaining obvious rows.
	if _, err := d.AddResidue(op.ID, "host_marker", "t", "C:/marker", "", ResidueRankObvious, true); err != nil {
		t.Fatal(err)
	}
	if n, err := d.KeepAllResidue(op.ID); err != nil || n == 0 {
		t.Fatalf("keep_all n=%d err=%v", n, err)
	}
	if _, blocked, _ := d.ResidueCompletionGate(op.ID); blocked {
		t.Fatal("keep_all must release the gate")
	}
}

func TestResidueIsTaskScoped(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	a := residueTask(t, d, TaskModeOp)
	b := residueTask(t, d, TaskModeOp)
	if _, err := d.AddResidue(a.ID, "k", "t", "l", "", ResidueRankObvious, true); err != nil {
		t.Fatal(err)
	}
	if _, blocked, _ := d.ResidueCompletionGate(b.ID); blocked {
		t.Fatal("other task's residue must not gate")
	}
	rows, err := d.ListResidue(b.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("leaked rows: %+v", rows)
	}
}
