package traffic

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is the budget's clock seam: the limiter is time-based, and these tests
// must not spend wall-clock seconds proving it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newBudget returns an on budget with a fake clock, bypassing Traffic.
func newBudget(rps float64, burst int) (*budget, *fakeClock) {
	c := newFakeClock()
	b := &budget{now: c.now}
	b.set(rps, burst)
	return b, c
}

func TestBudgetBurstThenRefill(t *testing.T) {
	b, clock := newBudget(10, 3) // 100ms per token, 3 in hand
	now := clock.now()
	for i := 0; i < 3; i++ {
		if w := b.take("a.example.com", now); w != 0 {
			t.Fatalf("burst take %d waited %v, want 0", i, w)
		}
	}
	if w := b.take("a.example.com", now); w != 100*time.Millisecond {
		t.Fatalf("take past burst waited %v, want 100ms", w)
	}
	// Refill is proportional to elapsed time and capped at the burst.
	if w := b.take("a.example.com", now.Add(250*time.Millisecond)); w != 0 {
		t.Fatalf("take after refill waited %v, want 0", w)
	}
	// Long idle: the bucket refilled to exactly the burst, no further.
	clock.advance(10 * time.Second)
	later := clock.now()
	for i := 0; i < 3; i++ {
		if w := b.take("a.example.com", later); w != 0 {
			t.Fatalf("take %d after idle waited %v, want 0", i, w)
		}
	}
	if w := b.take("a.example.com", later); w != 100*time.Millisecond {
		t.Fatalf("take past capped burst waited %v, want 100ms", w)
	}
}

func TestBudgetBurstDefaults(t *testing.T) {
	b, clock := newBudget(1, 0)
	now := clock.now()
	for i := 0; i < budgetDefaultBurst; i++ {
		if w := b.take("a.example.com", now); w != 0 {
			t.Fatalf("take %d waited %v, want 0", i, w)
		}
	}
	if w := b.take("a.example.com", now); w != time.Second {
		t.Fatalf("take past default burst waited %v, want 1s", w)
	}
}

func TestBudgetWaitIsCapped(t *testing.T) {
	b, clock := newBudget(0.1, 1) // one token in hand, then 10s per token
	now := clock.now()
	if w := b.take("a.example.com", now); w != 0 {
		t.Fatalf("burst take waited %v, want 0", w)
	}
	if w := b.take("a.example.com", now); w != budgetMaxWait {
		t.Fatalf("second take waited %v, want the %v cap", w, budgetMaxWait)
	}
}

func TestBudgetSeparateHostsAreIndependent(t *testing.T) {
	b, clock := newBudget(1, 1)
	now := clock.now()
	if w := b.take("a.example.com", now); w != 0 {
		t.Fatalf("first host waited %v", w)
	}
	if w := b.take("a.example.com", now); w != time.Second {
		t.Fatalf("first host second take waited %v, want 1s", w)
	}
	if w := b.take("b.example.com", now); w != 0 {
		t.Fatalf("second host waited %v, want its own bucket", w)
	}
}

func TestBudgetDisabledClearsState(t *testing.T) {
	b, clock := newBudget(1, 2)
	now := clock.now()
	b.take("a.example.com", now)
	b.observe("a.example.com", 429, now)
	if !b.on.Load() {
		t.Fatal("budget should be on")
	}
	b.set(0, 5)
	if b.on.Load() {
		t.Fatal("0 rps must disable the budget")
	}
	if w := b.take("a.example.com", clock.now()); w != 0 {
		t.Fatalf("take while disabled waited %v", w)
	}
	if got := len(b.hosts); got != 0 {
		t.Fatalf("state not cleared: %d hosts", got)
	}
}

func TestBudgetCooldownOn429(t *testing.T) {
	b, clock := newBudget(4, 4)
	now := clock.now()
	b.take("a.example.com", now)
	b.observe("a.example.com", 429, now)
	snap := b.snapshot(now.Add(time.Second))
	hb := snap["a.example.com"]
	if hb.S429 != 1 || hb.LastStatus != 429 {
		t.Fatalf("counters not recorded: %+v", hb)
	}
	if want := now.Add(budgetCooldown).Unix(); hb.CooldownUntil != want {
		t.Fatalf("cooldown_until = %d, want %d", hb.CooldownUntil, want)
	}
	if hb.Rate != 2 { // halved from 4
		t.Fatalf("rate = %v, want 2", hb.Rate)
	}
	// A cooling host waits, but never past the cap.
	if w := b.take("a.example.com", now.Add(time.Second)); w != budgetMaxWait {
		t.Fatalf("take during cooldown waited %v, want the %v cap", w, budgetMaxWait)
	}
	if w := b.take("a.example.com", now.Add(budgetCooldown)); w != 0 {
		t.Fatalf("take after cooldown waited %v, want 0", w)
	}
}

func TestBudgetCooldownOnTripleForbidden(t *testing.T) {
	b, clock := newBudget(4, 4)
	now := clock.now()
	for i := 0; i < 2; i++ {
		b.observe("a.example.com", 403, now)
		if hb := b.snapshot(now)["a.example.com"]; hb.CooldownUntil != 0 {
			t.Fatalf("cooldown started after %d 403s", i+1)
		}
	}
	// A success clears the streak, so the next 403 alone must not burn.
	b.observe("a.example.com", 200, now)
	b.observe("a.example.com", 403, now)
	if hb := b.snapshot(now)["a.example.com"]; hb.CooldownUntil != 0 {
		t.Fatal("403 after a success must not start a cooldown")
	}
	b.observe("a.example.com", 403, now)
	b.observe("a.example.com", 403, now)
	hb := b.snapshot(now)["a.example.com"]
	if hb.CooldownUntil == 0 {
		t.Fatal("third consecutive 403 must start a cooldown")
	}
	if hb.S403 != 5 {
		t.Fatalf("403 count = %d, want 5", hb.S403)
	}
	if hb.Rate != 2 {
		t.Fatalf("rate = %v, want 2 (halved)", hb.Rate)
	}
}

func TestBudgetPenaltyLapses(t *testing.T) {
	b, clock := newBudget(4, 4)
	now := clock.now()
	b.observe("a.example.com", 429, now)
	// Halved at most twice, then the rate restores after the penalty window.
	b.observe("a.example.com", 429, now)
	b.observe("a.example.com", 429, now)
	if got := b.snapshot(now)["a.example.com"].Rate; got != 1 {
		t.Fatalf("rate after three burns = %v, want 1 (never below rps/4)", got)
	}
	if got := b.snapshot(now.Add(budgetPenalty))["a.example.com"].Rate; got != 4 {
		t.Fatalf("rate after the penalty = %v, want 4", got)
	}
}

func TestBudgetSnapshotCountersAndDisabled(t *testing.T) {
	tr := &Traffic{}
	if got := tr.BudgetSnapshot(); got != nil {
		t.Fatalf("snapshot while off = %v, want nil", got)
	}
	clock := newFakeClock()
	tr.budget.now = clock.now
	tr.SetBudget(2, 2)
	if !tr.BudgetOn() {
		t.Fatal("BudgetOn = false after SetBudget")
	}
	tr.budget.take("a.example.com", clock.now())
	tr.budget.observe("a.example.com", 500, clock.now())
	snap := tr.BudgetSnapshot()
	hb, ok := snap["a.example.com"]
	if !ok {
		t.Fatal("host missing from snapshot")
	}
	if hb.Host != "a.example.com" || hb.Requests != 1 || hb.LastStatus != 500 || hb.Rate != 2 {
		t.Fatalf("snapshot = %+v", hb)
	}
	tr.SetBudget(0, 0)
	if tr.BudgetOn() || tr.BudgetSnapshot() != nil {
		t.Fatal("SetBudget(0) neither disabled nor cleared")
	}
}

// TestBudgetSinkHooks covers the addon wiring: Request accounts a token before
// forwarding, Response folds the status in — both with capture off, so no
// database is involved at all.
func TestBudgetSinkHooks(t *testing.T) {
	tr := &Traffic{}
	clock := newFakeClock()
	tr.budget.now = clock.now
	s := &sink{t: tr}

	// Off: the request hook is a no-op.
	s.Request(newFlow("a.example.com", "GET", "/", nil, nil))
	if len(tr.BudgetSnapshot()) != 0 {
		t.Fatal("budget off must not track hosts")
	}

	tr.SetBudget(2, 1)
	s.Request(newFlow("a.example.com", "GET", "/", nil, nil))
	s.Response(newFlow("a.example.com", "GET", "/", nil, nil))
	snap := tr.BudgetSnapshot()
	hb := snap["a.example.com"]
	if hb.Requests != 1 || hb.LastStatus != 200 {
		t.Fatalf("hooks did not report through: %+v", hb)
	}
}

func TestRecordSkippedWhenCaptureOff(t *testing.T) {
	tr, _ := openTraffic(t)
	defer tr.Close()
	if !tr.CaptureOn() {
		t.Fatal("Open must default capture on")
	}
	tr.SetCapture(false)
	tr.record(newFlow("a.example.com", "GET", "/off", nil, nil))
	if n, err := tr.Count(); err != nil || n != 0 {
		t.Fatalf("capture off recorded %d rows (err %v), want 0", n, err)
	}
	if got := tr.seq.Load(); got != 0 {
		t.Fatalf("capture off advanced seq to %d", got)
	}
	// Capture back on: recording resumes with the ordinary path.
	tr.SetCapture(true)
	tr.record(newFlow("a.example.com", "GET", "/on", nil, nil))
	if n, _ := tr.Count(); n != 1 {
		t.Fatalf("capture on recorded %d rows, want 1", n)
	}
}

// TestBudgetConcurrentTake exercises the hot path under contention so `-race` can
// see the map. Tokens are plentiful, so no test goroutine actually sleeps long.
func TestBudgetConcurrentTake(t *testing.T) {
	tr := &Traffic{}
	tr.SetBudget(1000, 1000)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := fmt.Sprintf("h%d.example.com", i%4)
			for j := 0; j < 20; j++ {
				tr.take(host)
				tr.budget.observe(host, 200, tr.budget.clock())
			}
		}(i)
	}
	wg.Wait()
	total := int64(0)
	for _, hb := range tr.BudgetSnapshot() {
		total += hb.Requests
	}
	if want := int64(32 * 20); total != want {
		t.Fatalf("requests = %d, want %d", total, want)
	}
}

func TestBudgetPrunesIdleHosts(t *testing.T) {
	b, clock := newBudget(1, 1)
	now := clock.now()
	for i := 0; i < maxBudgetHosts+10; i++ {
		b.take(fmt.Sprintf("h%d.example.com", i), now)
	}
	if got := len(b.hosts); got > maxBudgetHosts {
		t.Fatalf("host map grew to %d, want <= %d", got, maxBudgetHosts)
	}
}

func TestBudgetPruneDropsCoolingHostsAtTheBound(t *testing.T) {
	b, clock := newBudget(1, 1)
	now := clock.now()
	for i := 0; i < maxBudgetHosts; i++ {
		h := fmt.Sprintf("h%d.example.com", i)
		b.take(h, now)
		b.observe(h, 429, now) // every host cooling: no idle, no clear cooldown
	}
	b.pruneLocked(now)
	if got := len(b.hosts); got >= maxBudgetHosts {
		t.Fatalf("host map stayed at %d", got)
	}
}

func TestBudgetRateChangeKeepsCountersAndClampsCredit(t *testing.T) {
	b, clock := newBudget(10, 10)
	now := clock.now()
	b.take("a.example.com", now)
	b.set(1, 2)
	hb := b.snapshot(now)["a.example.com"]
	if hb.Requests != 1 || hb.Rate != 1 {
		t.Fatalf("counters lost on rate change: %+v", hb)
	}
	if bk := b.hosts["a.example.com"]; bk.tokens > 2 {
		t.Fatalf("tokens = %v, want clamped to the new burst 2", bk.tokens)
	}
}

func TestBudgetResponseKeepsRefillClock(t *testing.T) {
	b, clock := newBudget(2, 1)
	now := clock.now()
	b.take("a.example.com", now)
	if bk := b.hosts["a.example.com"]; bk.tokens != 0 {
		t.Fatalf("tokens after the burst take = %v, want 0", bk.tokens)
	}
	// A response 500ms later must credit that half-second, not just move the clock.
	b.observe("a.example.com", 200, now.Add(500*time.Millisecond))
	if bk := b.hosts["a.example.com"]; bk.tokens != 1 {
		t.Fatalf("tokens after the response = %v, want the 500ms credit", bk.tokens)
	}
	if w := b.take("a.example.com", now.Add(500*time.Millisecond)); w != 0 {
		t.Fatalf("take after the credited response waited %v", w)
	}
}

func TestBudgetTakeSkipsEmptyHost(t *testing.T) {
	b, clock := newBudget(1, 1)
	if w := b.take("", clock.now()); w != 0 {
		t.Fatalf("empty host waited %v", w)
	}
	if _, ok := b.hosts[""]; ok {
		t.Fatal("empty host keyed a bucket")
	}
}
