package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/egress"
	"github.com/Autumn-27/artex/traffic"
)

// nowFakeEgress is a managed provider that also implements the optional rotation
// capability, so the board's narrow type assertion is exercised without a CLI.
type nowFakeEgress struct {
	status   egress.Status
	rotating bool
}

func (f nowFakeEgress) Status(context.Context) (egress.Status, error) { return f.status, nil }
func (f nowFakeEgress) Rotate(context.Context, string) (egress.Status, error) {
	return f.status, nil
}
func (f nowFakeEgress) Rotating() bool { return f.rotating }

// nowTaskFixture creates an active op task and returns its SQL id.
func nowTaskFixture(t *testing.T, s *Server) *Task {
	t.Helper()
	mode := db.TaskModeOp
	task, err := s.m.CreateTaskWithOptions("now board", "local fixture", db.TaskCreateOptions{
		Name: "now board op",
		Mode: &mode,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { _ = s.m.pg.DeleteTask(taskID) })
	if _, err := s.m.pg.SetTaskPhase(taskID, db.TaskPhaseActive); err != nil {
		t.Fatal(err)
	}
	if err := s.m.SetTaskStatus(task.ID, "running"); err != nil {
		t.Fatal(err)
	}
	return task
}

// TestNowEmptyStateShape pins the empty board: arrays are present as arrays, not
// null, so a first poll renders without special cases.
func TestNowEmptyStateShape(t *testing.T) {
	board := buildNowBoard(nowInput{
		egress:      egress.Status{Driver: "none", Connected: true, Lockdown: true},
		budget:      map[string]traffic.HostBudget{},
		workers:     map[string]int{},
		hostWorkers: map[int64]map[string]int{},
		leases:      map[int64]map[string]int{},
		residue:     map[int64]int{},
	})

	raw, err := json.Marshal(board)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	tasks, ok := out["tasks"].([]any)
	if !ok || len(tasks) != 0 {
		t.Fatalf("tasks = %v, want an empty array", out["tasks"])
	}
	budget, ok := out["budget"].(map[string]any)
	if !ok {
		t.Fatalf("budget = %v", out["budget"])
	}
	if hosts, ok := budget["hosts"].([]any); !ok || len(hosts) != 0 {
		t.Fatalf("budget hosts = %v, want an empty array", budget["hosts"])
	}
	egress, ok := out["egress"].(map[string]any)
	if !ok {
		t.Fatalf("egress = %v", out["egress"])
	}
	if egress["driver"] != "none" || egress["managed"] != false {
		t.Fatalf("unmanaged egress = %v, want driver none", egress)
	}
	if egress["safe"] != true {
		t.Fatalf("unmanaged egress safe = %v, want true", egress["safe"])
	}
}

// TestNowOpTaskRollup covers the board's core rollup: an op task with two running
// intents on one host, one held lease and one open obvious residue.
func TestNowOpTaskRollup(t *testing.T) {
	s, _, token := opsecTestServer(t)
	task := nowTaskFixture(t, s)
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	host := "now-board.example"

	var assetID int64
	if err := s.m.pg.QueryRow(`INSERT INTO assets(type,domain,root_domain)
VALUES ('subdomain',$1,$1) RETURNING id`, host).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })

	ts := s.m.pg.Exploration(task.ExpID)
	payload := map[string]any{"summary": "now host intent", "asset_ids": []int64{assetID}}
	for i := 0; i < 2; i++ {
		intentID, err := ts.AddNode("intent", payload, 0, "running", "planner", []int64{assetID})
		if err != nil {
			t.Fatal(err)
		}
		s.engine.registerTaskWork(task.ID, intentID, func(error) {})
		t.Cleanup(func() {
			_, complete := s.engine.detachWork(intentID)
			complete(nil)
		})
	}

	if ok, err := s.m.pg.TryLeaseHost(taskID, host, "now-worker", db.DefaultHostLeaseTTL); err != nil || !ok {
		t.Fatalf("lease %s: ok=%v err=%v", host, ok, err)
	}

	if _, err := s.m.pg.AddResidue(taskID, "user", host, "now-board-obvious", "", db.ResidueRankObvious, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.AddResidue(taskID, "user", host, "now-board-plausible", "", db.ResidueRankPlausible, true); err != nil {
		t.Fatal(err)
	}
	removed, err := s.m.pg.AddResidue(taskID, "user", host, "now-board-removed", "", db.ResidueRankObvious, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.RemoveResidue(removed); err != nil {
		t.Fatal(err)
	}

	code, out := doJSON(t, s, token, http.MethodGet, "/api/now", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, out)
	}
	got := nowTaskFromResponse(t, out, task.ID)
	if got["mode"] != db.TaskModeOp || got["phase"] != db.TaskPhaseActive || got["status"] != "running" {
		t.Fatalf("task = %v", got)
	}
	if got["name"] != "now board op" {
		t.Fatalf("name = %v", got["name"])
	}
	if got["workers"] != float64(2) {
		t.Fatalf("workers = %v, want 2", got["workers"])
	}
	if got["residue_open"] != float64(1) {
		t.Fatalf("residue_open = %v, want 1", got["residue_open"])
	}

	hosts, ok := got["hosts"].([]any)
	if !ok || len(hosts) != 1 {
		t.Fatalf("hosts = %v", got["hosts"])
	}
	h, _ := hosts[0].(map[string]any)
	if h["host"] != host || h["workers"] != float64(2) || h["leases"] != float64(1) {
		t.Fatalf("host row = %v", h)
	}
	if h["requests"] != float64(0) || h["s429"] != float64(0) || h["cooldown_until"] != float64(0) {
		t.Fatalf("budget fields off = %v", h)
	}
}

// TestNowBudgetHostMapping checks snapshot-to-DTO mapping and how per-task hosts
// inherit the shared counters.
func TestNowBudgetHostMapping(t *testing.T) {
	snap := map[string]traffic.HostBudget{
		"api.example.com": {
			Host: "api.example.com", Requests: 12, S429: 3, S403: 1,
			LastStatus: 429, CooldownUntil: 1700000000,
		},
		"z.example.com": {
			Host: "z.example.com", Requests: 12, S429: 0, S403: 0, LastStatus: 200,
		},
		"quiet.example.com": {
			Host: "quiet.example.com", Requests: 2, S429: 0, S403: 0, LastStatus: 200,
		},
	}

	budget := nowBudgetHosts(snap)
	if len(budget) != 3 {
		t.Fatalf("budget hosts = %+v", budget)
	}
	if budget[0].Host != "api.example.com" || budget[1].Host != "z.example.com" {
		t.Fatalf("budget sort = %+v", budget)
	}
	if budget[0].Requests != 12 || budget[0].S429 != 3 || budget[0].S403 != 1 ||
		budget[0].LastStatus != 429 || budget[0].CooldownUntil != 1700000000 {
		t.Fatalf("budget row = %+v", budget[0])
	}

	hosts := nowTaskHosts(
		map[string]int{"api.example.com": 2, "quiet.example.com": 0},
		map[string]int{"api.example.com": 1, "quiet.example.com": 1},
		snap,
	)
	if len(hosts) != 2 {
		t.Fatalf("task hosts = %+v", hosts)
	}
	if hosts[0].Host != "api.example.com" || hosts[0].Workers != 2 ||
		hosts[0].Leases != 1 || hosts[0].Requests != 12 ||
		hosts[0].S429 != 3 || hosts[0].CooldownUntil != 1700000000 {
		t.Fatalf("task host budget mapping = %+v", hosts[0])
	}
	// A quiet host has no running intent but still has a lease: it must be shown.
	if hosts[1].Host != "quiet.example.com" || hosts[1].Workers != 0 || hosts[1].Leases != 1 {
		t.Fatalf("lease-only host = %+v", hosts[1])
	}
}

// TestNowManagedEgressRotating checks the managed identity shape and the optional
// rotation flag the rotation slice will add.
func TestNowManagedEgressRotating(t *testing.T) {
	s, _, token := opsecTestServer(t)
	s.m.mu.Lock()
	s.m.egress = nowFakeEgress{
		status: egress.Status{
			Driver: "mullvad", Connected: true, Lockdown: true,
			ExitIP: "203.0.113.9", City: "Stockholm", Country: "Sweden",
		},
		rotating: true,
	}
	s.m.mu.Unlock()

	code, out := doJSON(t, s, token, http.MethodGet, "/api/now", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, out)
	}
	egress, _ := out["egress"].(map[string]any)
	if egress["driver"] != "mullvad" || egress["managed"] != true {
		t.Fatalf("egress = %v", egress)
	}
	if egress["connected"] != true || egress["lockdown"] != true || egress["safe"] != true {
		t.Fatalf("egress safety = %v", egress)
	}
	if egress["exit_ip"] != "203.0.113.9" || egress["city"] != "Stockholm" || egress["country"] != "Sweden" {
		t.Fatalf("egress identity = %v", egress)
	}
	if egress["rotating"] != true {
		t.Fatalf("rotating = %v, want true", egress["rotating"])
	}
}

// TestNowUnmanagedEgressDriverNone checks an unmanaged box reports a safe "none"
// identity rather than an empty object.
func TestNowUnmanagedEgressDriverNone(t *testing.T) {
	s, _, token := opsecTestServer(t)
	s.m.mu.Lock()
	s.m.egress = nil
	s.m.mu.Unlock()

	code, out := doJSON(t, s, token, http.MethodGet, "/api/now", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, out)
	}
	egress, _ := out["egress"].(map[string]any)
	if egress["driver"] != "none" || egress["managed"] != false {
		t.Fatalf("egress = %v, want driver none", egress)
	}
	if egress["connected"] != true || egress["lockdown"] != true || egress["safe"] != true {
		t.Fatalf("egress safety = %v", egress)
	}
	if egress["rotating"] != false {
		t.Fatalf("rotating = %v, want false", egress["rotating"])
	}
	for _, field := range []string{"exit_ip", "city", "country"} {
		if egress[field] != "" {
			t.Fatalf("%s = %v, want empty", field, egress[field])
		}
	}
}

func nowTaskFromResponse(t *testing.T, out map[string]any, id string) map[string]any {
	t.Helper()
	tasks, ok := out["tasks"].([]any)
	if !ok {
		t.Fatalf("tasks = %v", out["tasks"])
	}
	for _, raw := range tasks {
		task, _ := raw.(map[string]any)
		if task["id"] == id {
			return task
		}
	}
	t.Fatalf("task %s missing from %v", id, tasks)
	return nil
}
