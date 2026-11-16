package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/dbtest"
)

func opsecTestServer(t *testing.T) (*Server, *Task, string) {
	t.Helper()
	dbtest.DSN(t) // skip unless ARTEX_TEST_DSN points at a throwaway DB
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { m.Close() })
	task, err := m.CreateTask("opsec http", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { _ = m.pg.DeleteTask(taskID) })
	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	return s, task, token
}

func doJSON(t *testing.T, s *Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestTaskCreateOpsecFields(t *testing.T) {
	s, _, token := opsecTestServer(t)
	code, out := doJSON(t, s, token, http.MethodPost, "/api/tasks", map[string]any{
		"description": "opsec create",
		"goal":        "goal",
		"mode":        "op",
		"cover_story": "  contractor doing QuickBooks token integration  ",
		"house_style": map[string]any{"branch": "^(DEV|TS)-[0-9]+$"},
		"stealth":     map[string]any{"rps": 1, "burst": 5},
	})
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, out)
	}
	if out["mode"] != db.TaskModeOp || out["cover_story"] != "contractor doing QuickBooks token integration" {
		t.Fatalf("create did not normalize opsec: %v", out)
	}
	if house, _ := out["house_style"].(map[string]any); house["branch"] != "^(DEV|TS)-[0-9]+$" {
		t.Fatalf("house_style not returned: %v", out["house_style"])
	}
	if stealth, _ := out["stealth"].(map[string]any); stealth["rps"] != float64(1) || stealth["burst"] != float64(5) {
		t.Fatalf("stealth not returned: %v", out["stealth"])
	}
	id, _ := out["id"].(string)
	code, out = doJSON(t, s, token, http.MethodGet, "/api/tasks/"+id, nil)
	if code != http.StatusOK || out["mode"] != db.TaskModeOp {
		t.Fatalf("get status=%d body=%v", code, out)
	}
}

func TestTaskCreateOpsecDefaults(t *testing.T) {
	s, _, token := opsecTestServer(t)
	code, out := doJSON(t, s, token, http.MethodPost, "/api/tasks", map[string]any{
		"description": "opsec defaults", "goal": "goal",
	})
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, out)
	}
	if out["mode"] != db.TaskModePentest || out["cover_story"] != "" {
		t.Fatalf("create must default to pentest without a cover story: %v", out)
	}
	if house, ok := out["house_style"].(map[string]any); !ok || len(house) != 0 {
		t.Fatalf("house_style default = %v", out["house_style"])
	}
	if stealth, ok := out["stealth"].(map[string]any); !ok || len(stealth) != 0 {
		t.Fatalf("stealth default = %v", out["stealth"])
	}
}

func TestTaskCreateOpsecValidation(t *testing.T) {
	s, _, token := opsecTestServer(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "mode enum", body: map[string]any{"mode": "scan"}},
		{name: "cover story length", body: map[string]any{"cover_story": strings.Repeat("任", maxTaskNameRunes+1)}},
		{name: "house style shape", body: map[string]any{"house_style": []string{"branch"}}},
		{name: "stealth shape", body: map[string]any{"stealth": "1rps"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.body["description"] = "opsec validation"
			tc.body["goal"] = "goal"
			if code, out := doJSON(t, s, token, http.MethodPost, "/api/tasks", tc.body); code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v", code, out)
			}
		})
	}
}

func TestTaskOpsecPatchAuthAndValidation(t *testing.T) {
	s, task, token := opsecTestServer(t)
	path := "/api/tasks/" + task.ID + "/opsec"

	if code, _ := doJSON(t, s, "", http.MethodPatch, path, map[string]any{"mode": "op"}); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated patch status=%d", code)
	}
	if code, _ := doJSON(t, s, token, http.MethodPatch, "/api/tasks/999999999/opsec", map[string]any{"mode": "op"}); code != http.StatusNotFound {
		t.Fatalf("missing task patch status=%d", code)
	}
	for _, body := range []map[string]any{
		{},
		{"mode": "scan"},
		{"cover_story": strings.Repeat("任", maxTaskNameRunes+1)},
		{"house_style": []string{"branch"}},
		{"stealth": 1},
	} {
		if code, out := doJSON(t, s, token, http.MethodPatch, path, body); code != http.StatusBadRequest {
			t.Fatalf("body %v status=%d out=%v", body, code, out)
		}
	}
	if code, out := doJSON(t, s, token, http.MethodPatch, path, map[string]any{"mode": "bogus"}); code != http.StatusBadRequest {
		t.Fatalf("invalid mode status=%d out=%v", code, out)
	}

	code, out := doJSON(t, s, token, http.MethodPatch, path, map[string]any{
		"mode":        "op",
		"cover_story": "  support triage  ",
		"house_style": map[string]any{"branch": "^(DEV|TS)-[0-9]+$"},
		"stealth":     map[string]any{"rps": 1},
	})
	if code != http.StatusOK {
		t.Fatalf("patch status=%d body=%v", code, out)
	}
	if out["mode"] != db.TaskModeOp || out["cover_story"] != "support triage" {
		t.Fatalf("patch did not normalize opsec: %v", out)
	}
	if house, _ := out["house_style"].(map[string]any); house["branch"] != "^(DEV|TS)-[0-9]+$" {
		t.Fatalf("house_style not patched: %v", out["house_style"])
	}

	stored, err := s.m.pg.GetTask(mustTaskID(t, task.ID))
	if err != nil || stored == nil {
		t.Fatalf("stored task = %+v, %v", stored, err)
	}
	if stored.Mode != db.TaskModeOp || stored.CoverStory != "support triage" || string(stored.Stealth) != `{"rps": 1}` {
		t.Fatalf("patch not persisted: %+v", stored)
	}

	// Empty mode resets to the default; JSON null clears to the empty object.
	code, out = doJSON(t, s, token, http.MethodPatch, path, map[string]any{"mode": "", "house_style": nil, "stealth": nil})
	if code != http.StatusOK {
		t.Fatalf("reset patch status=%d body=%v", code, out)
	}
	if out["mode"] != db.TaskModePentest || out["cover_story"] != "support triage" {
		t.Fatalf("reset patch = %v", out)
	}
	if house, _ := out["house_style"].(map[string]any); len(house) != 0 {
		t.Fatalf("house_style reset = %v", out["house_style"])
	}
	if stealth, _ := out["stealth"].(map[string]any); len(stealth) != 0 {
		t.Fatalf("stealth reset = %v", out["stealth"])
	}
}

// TestAgentsForTaskProfileFollowsMode pins the server wiring: the prompt builders
// must read the task row's engagement profile, not a value fixed at creation.
func TestAgentsForTaskProfileFollowsMode(t *testing.T) {
	s, task, _ := opsecTestServer(t)
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	bundle := s.agentsForTask(task)
	if got := bundle.wk.Profile(); got != db.TaskModePentest {
		t.Fatalf("worker profile = %q, want pentest by default", got)
	}
	if got := bundle.pl.Profile(); got != db.TaskModePentest {
		t.Fatalf("planner profile = %q, want pentest by default", got)
	}
	if got := bundle.main.Profile(); got != db.TaskModePentest {
		t.Fatalf("main profile = %q, want pentest by default", got)
	}
	mode := db.TaskModeOp
	if _, err := s.m.pg.UpdateTask(taskID, db.TaskPatch{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	if got := s.agentsForTask(task).wk.Profile(); got != db.TaskModeOp {
		t.Fatalf("worker profile after switch = %q, want op", got)
	}
}

// TestResidueBlocksCompletion pins the completion gate: pentest never blocks, op
// blocks on obvious removable residue, and keep_all releases it.
func TestResidueBlocksCompletion(t *testing.T) {
	s, task, _ := opsecTestServer(t)
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if s.engine.residueBlocks(task) {
		t.Fatal("pentest must not block completion")
	}
	mode := db.TaskModeOp
	if _, err := s.m.pg.UpdateTask(taskID, db.TaskPatch{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.AddResidue(taskID, "github_branch", "acme/widgets", "refs/heads/DEV-1-probe", "", db.ResidueRankObvious, true); err != nil {
		t.Fatal(err)
	}
	if !s.engine.residueBlocks(task) {
		t.Fatal("open obvious residue must block completion")
	}
	if _, err := s.m.pg.KeepAllResidue(taskID); err != nil {
		t.Fatal(err)
	}
	if s.engine.residueBlocks(task) {
		t.Fatal("keep_all must release the gate")
	}
}

// TestClaimNextHonoursHostLeases pins the scheduler half: with host_concurrency 1
// a second worker cannot claim an intent for the same host, and releasing the
// first makes it claimable again.
func TestClaimNextHonoursHostLeases(t *testing.T) {
	s, task, _ := opsecTestServer(t)
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	mode := db.TaskModeOp
	if _, err := s.m.pg.UpdateTask(taskID, db.TaskPatch{Mode: &mode, Stealth: []byte(`{"host_concurrency":1}`)}); err != nil {
		t.Fatal(err)
	}
	assetID, err := s.m.assets.UpsertSubdomain(db.UpsertSubdomainReq{Domain: "lease.example"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	mk := func(summary string) int64 {
		id, err := task.Store.AddIntent(map[string]any{
			"summary":   summary,
			"asset_ids": []any{assetID},
		}, 5, nil, "planner")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mk("first host intent")
	mk("second host intent")

	first, leases, ok := s.engine.claimNext(task, "work#1")
	if !ok || first == nil {
		t.Fatal("first claim should succeed")
	}
	if len(leases) != 1 {
		t.Fatalf("leases=%v", leases)
	}
	if _, _, ok := s.engine.claimNext(task, "work#2"); ok {
		t.Fatal("second worker must be denied while the host slot is held")
	}
	s.engine.releaseHostLeases(task, leases, "work#1")
	if next, l2, ok := s.engine.claimNext(task, "work#2"); !ok || next == nil {
		t.Fatal("released slot must become claimable")
	} else {
		s.engine.releaseHostLeases(task, l2, "work#2")
	}
}

// TestClaimNextIgnoresLeasesForPentest keeps historical behaviour.
func TestClaimNextIgnoresLeasesForPentest(t *testing.T) {
	s, task, _ := opsecTestServer(t)
	assetID, err := s.m.assets.UpsertSubdomain(db.UpsertSubdomainReq{Domain: "free.example"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	for i := 0; i < 2; i++ {
		if _, err := task.Store.AddIntent(map[string]any{
			"summary":   "pentest host intent",
			"asset_ids": []any{assetID},
		}, 5, nil, "planner"); err != nil {
			t.Fatal(err)
		}
	}
	if _, leases, ok := s.engine.claimNext(task, "work#1"); !ok || len(leases) != 0 {
		t.Fatalf("pentest claim ok=%v leases=%v, want no lease", ok, leases)
	}
	if _, _, ok := s.engine.claimNext(task, "work#2"); !ok {
		t.Fatal("pentest must allow two workers on one host")
	}
}

// TestPassivePhaseHoldsActiveIntents pins the phase gate: in passive only tagged
// intents are claimable, and phase_set(active) releases the rest.
func TestPassivePhaseHoldsActiveIntents(t *testing.T) {
	s, task, _ := opsecTestServer(t)
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	mode := db.TaskModeOp
	if _, err := s.m.pg.UpdateTask(taskID, db.TaskPatch{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.SetTaskPhase(taskID, db.TaskPhasePassive); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Store.AddIntent(map[string]any{"summary": "active probe"}, 9, nil, "planner"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.engine.claimNext(task, "work#1"); ok {
		t.Fatal("untagged intent must not be claimed during passive")
	}
	if _, err := task.Store.AddIntent(map[string]any{"summary": "ct logs", "phase": "passive"}, 5, nil, "planner"); err != nil {
		t.Fatal(err)
	}
	claimed, leases, ok := s.engine.claimNext(task, "work#1")
	if !ok || claimed == nil {
		t.Fatal("passive intent must be claimable")
	}
	s.engine.releaseHostLeases(task, leases, "work#1")
	if _, err := s.m.pg.SetTaskPhase(taskID, db.TaskPhaseActive); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.engine.claimNext(task, "work#2"); !ok {
		t.Fatal("active phase must release the untagged intent")
	}
}
