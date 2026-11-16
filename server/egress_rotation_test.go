package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/egress"
	"github.com/Autumn-27/artex/traffic"
)

// fakeEgressProvider is a stub egress.Provider: no mullvad binary, no network.
type fakeEgressProvider struct {
	mu        sync.Mutex
	status    egress.Status
	next      egress.Status
	rotateErr error
	rotations int
	locations []string
}

func (f *fakeEgressProvider) Status(context.Context) (egress.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}

func (f *fakeEgressProvider) Rotate(_ context.Context, location string) (egress.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotations++
	f.locations = append(f.locations, location)
	if f.rotateErr != nil {
		return f.status, f.rotateErr
	}
	if f.next.Driver == "" {
		f.next.Driver = "mullvad"
	}
	return f.next, nil
}

func (f *fakeEgressProvider) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rotations
}

func (f *fakeEgressProvider) seenLocations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.locations...)
}

// TestClaimNextHonorsRotationBarrier pins the wave boundary: a worker must not
// claim while the exit is changing. The guard runs before any store access, so a
// barrier hit is safe even without a live task store.
func TestClaimNextHonorsRotationBarrier(t *testing.T) {
	e := NewEngine(nil)
	e.SetRotating(true)
	if _, _, ok := e.claimNext(&Task{ID: "1"}, "work#1"); ok {
		t.Fatal("claimNext claimed an intent during egress rotation")
	}
	e.SetRotating(false)
	if e.Rotating() {
		t.Fatal("barrier did not clear")
	}
}

// TestPlannerRoundSkipsWhileRotating drives a round with a non-nil planner and no
// task store. Without the barrier it would reach HasOpenGoal and panic; with the
// barrier it returns before touching either the store or the provider.
func TestPlannerRoundSkipsWhileRotating(t *testing.T) {
	e := NewEngine(nil)
	e.debounce = 0
	e.SetRotating(true)
	e.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) {
		return agent.NewPlanner(nil, "test-model", t.TempDir(), nil, 0, 0), nil
	})
	task := &Task{ID: "1", Status: "running"}
	e.planRound(context.Background(), task, "test")
	if got := e.ActiveLLMCalls("1"); got != 0 {
		t.Fatalf("planner started %d LLM call(s) during rotation", got)
	}
	if _, ok := e.plannerRound.Load("1"); ok {
		t.Fatal("planner allocated a round number during rotation")
	}
}

func TestWaitIdleReturnsWhenIdle(t *testing.T) {
	e := NewEngine(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle on an idle engine: %v", err)
	}
}

func TestWaitIdleTimesOutUnderHeldWorkAndLLMCall(t *testing.T) {
	e := NewEngine(nil)
	e.registerWork(7, func(error) {})
	e.BeginLLMCall("1")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if err := e.WaitIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitIdle under a held work lease = %v, want deadline exceeded", err)
	}

	_, complete := e.detachWork(7)
	complete(nil)
	e.EndLLMCall("1")

	idleCtx, idleCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer idleCancel()
	if err := e.WaitIdle(idleCtx); err != nil {
		t.Fatalf("WaitIdle after drain: %v", err)
	}
}

// TestRotateEgressAbortsWhenDrainTimesOut proves the "never rotate under load"
// rule: the provider is not called and both barriers are cleared.
func TestRotateEgressAbortsWhenDrainTimesOut(t *testing.T) {
	provider := &fakeEgressProvider{
		status: egress.Status{Driver: "mullvad", Connected: true, ExitIP: "1.1.1.1"},
		next:   egress.Status{Driver: "mullvad", Connected: true, ExitIP: "2.2.2.2"},
	}
	m := &Manager{egress: provider, tasks: map[string]*Task{}}
	e := NewEngine(m)
	s := &Server{m: m, engine: e, egressRotateWait: 40 * time.Millisecond}

	e.BeginLLMCall("1")
	defer e.EndLLMCall("1")

	if _, err := s.RotateEgress(context.Background(), ""); err == nil {
		t.Fatal("rotation succeeded while a model call held the drain barrier")
	}
	if got := provider.calls(); got != 0 {
		t.Fatalf("provider rotated %d time(s) under load, want 0", got)
	}
	if e.Rotating() {
		t.Fatal("engine rotation barrier was not cleared after the abort")
	}
	if m.Rotating() {
		t.Fatal("manager rotation state was not cleared after the abort")
	}
}

// TestEgressBurnHostGating pins threshold, baseline, driver, enable and cooldown
// checks without sleeping or touching the fake provider's rotation path.
func TestEgressBurnHostGating(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	prevAuto, prevThreshold := s.m.EgressAutoRotate(), s.m.EgressBurnThreshold()
	t.Cleanup(func() {
		s.m.SetEgressAutoRotate(prevAuto)         //nolint:errcheck
		s.m.SetEgressBurnThreshold(prevThreshold) //nolint:errcheck
	})
	provider := &fakeEgressProvider{
		status: egress.Status{Driver: "mullvad", Connected: true, ExitIP: "1.1.1.1"},
		next:   egress.Status{Driver: "mullvad", Connected: true, ExitIP: "2.2.2.2"},
	}
	s.m.mu.Lock()
	prevProvider := s.m.egress
	s.m.egress = provider
	s.m.mu.Unlock()
	t.Cleanup(func() {
		s.m.mu.Lock()
		s.m.egress = prevProvider
		s.m.mu.Unlock()
	})
	if err := s.m.SetEgressAutoRotate(true); err != nil {
		t.Fatal(err)
	}
	if err := s.m.SetEgressBurnThreshold(50); err != nil {
		t.Fatal(err)
	}
	s.setEgressBurnBaseline(map[string]int64{"burned.example": 100})
	now := time.Now()
	snap := map[string]traffic.HostBudget{
		"burned.example": {Host: "burned.example", S429: 149},
	}
	if host, _, ok := s.egressBurnHost(snap, now); ok {
		t.Fatalf("host %s triggered below the threshold", host)
	}
	snap["burned.example"] = traffic.HostBudget{Host: "burned.example", S429: 150}
	host, delta, ok := s.egressBurnHost(snap, now)
	if !ok || host != "burned.example" || delta != 50 {
		t.Fatalf("threshold crossing = (%q,%d,%v), want (burned.example,50,true)", host, delta, ok)
	}

	// Auto-rotate off never triggers.
	if err := s.m.SetEgressAutoRotate(false); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.egressBurnHost(snap, now); ok {
		t.Fatal("auto-rotate off still triggered")
	}
	if err := s.m.SetEgressAutoRotate(true); err != nil {
		t.Fatal(err)
	}

	// No driver never triggers.
	s.m.mu.Lock()
	s.m.egress = nil
	s.m.mu.Unlock()
	if _, _, ok := s.egressBurnHost(snap, now); ok {
		t.Fatal("rotation triggered with no egress driver")
	}
	s.m.mu.Lock()
	s.m.egress = provider
	s.m.mu.Unlock()

	// The 15 minute cooldown suppresses a second rotation.
	if _, err := s.m.rotateEgress(context.Background(), "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.egressBurnHost(snap, now.Add(time.Minute)); ok {
		t.Fatal("rotation triggered inside the cooldown window")
	}
	if _, _, ok := s.egressBurnHost(snap, now.Add(egressAutoRotateCooldown+time.Second)); !ok {
		t.Fatal("rotation did not trigger after the cooldown expired")
	}
}

// TestMaybeAutoRotateTriggersProviderAndRecordsReason covers the full automatic
// path: decision, barrier, provider call and last-rotation audit.
func TestMaybeAutoRotateTriggersProviderAndRecordsReason(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	prevAuto, prevThreshold := s.m.EgressAutoRotate(), s.m.EgressBurnThreshold()
	t.Cleanup(func() {
		s.m.SetEgressAutoRotate(prevAuto)         //nolint:errcheck
		s.m.SetEgressBurnThreshold(prevThreshold) //nolint:errcheck
	})
	provider := &fakeEgressProvider{
		status: egress.Status{Driver: "mullvad", Connected: true, ExitIP: "1.1.1.1", City: "Frankfurt", Country: "Germany"},
		next:   egress.Status{Driver: "mullvad", Connected: true, ExitIP: "2.2.2.2", City: "Amsterdam", Country: "Netherlands"},
	}
	s.m.mu.Lock()
	prevProvider := s.m.egress
	s.m.egress = provider
	s.m.mu.Unlock()
	t.Cleanup(func() {
		s.m.mu.Lock()
		s.m.egress = prevProvider
		s.m.mu.Unlock()
	})
	if err := s.m.SetEgressAutoRotate(true); err != nil {
		t.Fatal(err)
	}
	if err := s.m.SetEgressBurnThreshold(50); err != nil {
		t.Fatal(err)
	}
	s.setEgressBurnBaseline(map[string]int64{"burned.example": 10})
	snap := map[string]traffic.HostBudget{
		"burned.example": {Host: "burned.example", S429: 70},
	}
	ran, err := s.maybeAutoRotate(context.Background(), snap, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("maybeAutoRotate did not rotate after the host crossed the threshold")
	}
	if got := provider.calls(); got != 1 {
		t.Fatalf("provider rotations = %d, want 1", got)
	}
	rot, ok := s.m.LastRotation()
	if !ok {
		t.Fatal("last rotation was not recorded")
	}
	if !strings.Contains(rot.Reason, "burn threshold") || !strings.Contains(rot.Reason, "burned.example") {
		t.Fatalf("last rotation reason = %q, want burn threshold for burned.example", rot.Reason)
	}
	if rot.Old.ExitIP != "1.1.1.1" || rot.New.ExitIP != "2.2.2.2" {
		t.Fatalf("last rotation exits = %s -> %s", rot.Old.ExitIP, rot.New.ExitIP)
	}
	if s.engine.Rotating() || s.m.Rotating() {
		t.Fatal("rotation barriers left set after auto-rotate")
	}
}

// TestRotateEgressEmitsActivityToRunningTasks checks one system activity per
// running task and none for a terminal one, with old->new in the summary.
func TestRotateEgressEmitsActivityToRunningTasks(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	tasks := s.m.List()
	if len(tasks) == 0 {
		t.Fatal("fixture did not create a task")
	}
	task := tasks[0]
	if err := s.m.SetTaskStatus(task.ID, "running"); err != nil {
		t.Fatal(err)
	}
	provider := &fakeEgressProvider{
		status: egress.Status{Driver: "mullvad", Connected: true, ExitIP: "1.1.1.1"},
		next:   egress.Status{Driver: "mullvad", Connected: true, ExitIP: "2.2.2.2"},
	}
	s.m.mu.Lock()
	s.m.egress = provider
	s.m.mu.Unlock()

	if _, err := s.RotateEgress(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	items, _, err := task.Store.ActivityList(nil, 0, 300)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, a := range items {
		if strings.Contains(a.Summary, "Egress rotated") && strings.Contains(a.Summary, "1.1.1.1") && strings.Contains(a.Summary, "2.2.2.2") {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("rotated activity count = %d, want 1 (items=%d)", found, len(items))
	}

	if err := s.m.SetTaskStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	before := len(items)
	if _, err := s.RotateEgress(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	items, _, err = task.Store.ActivityList(nil, 0, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != before {
		t.Fatalf("terminal task received %d new activity row(s)", len(items)-before)
	}
}

// TestEgressRoutesAndSettingsRoundTrip covers the HTTP surface: status, rotation,
// new settings keys and validation.
func TestEgressRoutesAndSettingsRoundTrip(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	prevAuto, prevThreshold := s.m.EgressAutoRotate(), s.m.EgressBurnThreshold()
	t.Cleanup(func() {
		s.m.SetEgressAutoRotate(prevAuto)         //nolint:errcheck
		s.m.SetEgressBurnThreshold(prevThreshold) //nolint:errcheck
	})
	provider := &fakeEgressProvider{
		status: egress.Status{Driver: "mullvad", Connected: true, ExitIP: "1.1.1.1"},
		next:   egress.Status{Driver: "mullvad", Connected: true, ExitIP: "2.2.2.2", City: "Amsterdam", Country: "Netherlands"},
	}
	s.m.mu.Lock()
	prevProvider := s.m.egress
	s.m.egress = provider
	s.m.mu.Unlock()
	t.Cleanup(func() {
		s.m.mu.Lock()
		s.m.egress = prevProvider
		s.m.mu.Unlock()
	})

	type egressResponse struct {
		Driver       string          `json:"driver"`
		ExitIP       string          `json:"exit_ip"`
		Rotating     bool            `json:"rotating"`
		LastRotation *EgressRotation `json:"last_rotation"`
	}

	r := request("GET", "/api/egress", "")
	if r.Code != 200 {
		t.Fatalf("GET /api/egress = %d %s", r.Code, r.Body)
	}
	var got egressResponse
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Driver != "mullvad" || got.ExitIP != "1.1.1.1" || got.Rotating || got.LastRotation != nil {
		t.Fatalf("initial egress payload = %+v", got)
	}

	r = request("POST", "/api/egress/rotate", `{"location":"de fra"}`)
	if r.Code != 200 {
		t.Fatalf("POST /api/egress/rotate = %d %s", r.Code, r.Body)
	}
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ExitIP != "2.2.2.2" || got.Rotating {
		t.Fatalf("rotation payload = %+v", got)
	}
	if got.LastRotation == nil || got.LastRotation.Old.ExitIP != "1.1.1.1" || got.LastRotation.New.ExitIP != "2.2.2.2" {
		t.Fatalf("last rotation = %+v", got.LastRotation)
	}
	if got.LastRotation.Reason != "operator" {
		t.Fatalf("last rotation reason = %q, want operator", got.LastRotation.Reason)
	}
	if locations := provider.seenLocations(); len(locations) != 1 || locations[0] != "de fra" {
		t.Fatalf("provider locations = %v, want [de fra]", locations)
	}

	// Defaults and a full round-trip for the two new keys.
	payload := s.settingsPayload()
	if payload["egress_auto_rotate"] != false || payload["egress_burn_threshold"] != defaultEgressBurnThreshold {
		t.Fatalf("egress burn defaults = %v/%v", payload["egress_auto_rotate"], payload["egress_burn_threshold"])
	}
	r = request("PUT", "/api/settings", `{"egress_auto_rotate":true,"egress_burn_threshold":75}`)
	if r.Code != 200 {
		t.Fatalf("PUT egress burn = %d %s", r.Code, r.Body)
	}
	if !s.m.EgressAutoRotate() || s.m.EgressBurnThreshold() != 75 {
		t.Fatalf("stored burn settings = %v/%d", s.m.EgressAutoRotate(), s.m.EgressBurnThreshold())
	}
	if v, _, _ := s.m.pg.GetSetting(settingEgressAutoRotate); v != "true" {
		t.Fatalf("setting %s = %q", settingEgressAutoRotate, v)
	}
	if v, _, _ := s.m.pg.GetSetting(settingEgressBurnThreshold); v != "75" {
		t.Fatalf("setting %s = %q", settingEgressBurnThreshold, v)
	}

	// Partial PUT preserves the other value, and a non-positive threshold is rejected.
	if r := request("PUT", "/api/settings", `{"egress_auto_rotate":false}`); r.Code != 200 {
		t.Fatalf("partial PUT = %d %s", r.Code, r.Body)
	}
	if s.m.EgressAutoRotate() || s.m.EgressBurnThreshold() != 75 {
		t.Fatalf("partial PUT changed the pair: %v/%d", s.m.EgressAutoRotate(), s.m.EgressBurnThreshold())
	}
	if r := request("PUT", "/api/settings", `{"egress_burn_threshold":0}`); r.Code != 400 {
		t.Fatalf("PUT threshold=0 = %d, want 400", r.Code)
	}
}
