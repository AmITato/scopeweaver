package db

import (
	"testing"

	"github.com/Autumn-27/artex/internal/dbtest"
)

func TestTaskPhaseDefaultsByMode(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()

	pentest, err := d.CreateTaskWithOptions("phase pentest", "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(pentest.ID) })
	if got := d.TaskPhase(pentest.ID); got != TaskPhaseActive {
		t.Fatalf("pentest phase = %q, want active", got)
	}

	op := TaskModeOp
	opTask, err := d.CreateTaskWithOptions("phase op", "goal", TaskCreateOptions{Mode: &op})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(opTask.ID) })
	if got := d.TaskPhase(opTask.ID); got != TaskPhasePassive {
		t.Fatalf("op phase = %q, want passive", got)
	}
}

func TestTaskPhaseSetValidation(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("phase set", "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	if _, err := d.SetTaskPhase(task.ID, "urgent"); err == nil {
		t.Fatal("unknown phase must fail")
	}
	if _, err := d.SetTaskPhase(0, TaskPhaseActive); err == nil {
		t.Fatal("missing task must fail")
	}
	got, err := d.SetTaskPhase(task.ID, TaskPhasePassive)
	if err != nil || got != TaskPhasePassive {
		t.Fatalf("set passive: %q %v", got, err)
	}
	if d.TaskPhase(task.ID) != TaskPhasePassive {
		t.Fatal("phase not persisted")
	}
	if d.TaskPhase(0) != TaskPhaseActive {
		t.Fatal("missing task must read active")
	}
}

func TestIntentPhaseTag(t *testing.T) {
	if got := IntentPhase(nil); got != TaskPhaseActive {
		t.Fatalf("nil intent = %q", got)
	}
	if got := IntentPhase(&Node{Payload: []byte(`{"summary":"x"}`)}); got != TaskPhaseActive {
		t.Fatalf("untagged intent = %q, want active", got)
	}
	if got := IntentPhase(&Node{Payload: []byte(`{"summary":"x","phase":"passive"}`)}); got != TaskPhasePassive {
		t.Fatalf("tagged intent = %q", got)
	}
	if got := IntentPhase(&Node{Payload: []byte(`{"phase":"nonsense"}`)}); got != TaskPhaseActive {
		t.Fatalf("bad tag = %q, want active", got)
	}
}
