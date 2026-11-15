package traffic

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Per-host egress budget (docs/opsec-blend.md §7.1-7.3). The MITM proxy applies a
// token bucket to every forwarded request, keyed by target host, so an op
// engagement can be throttled without recording a single exchange. The budget is
// deliberately per host, not per worker — fifty workers share one bucket, because
// the target sees one client.
//
// Everything below runs on the proxy's request hot path, so the state is a plain
// mutex-guarded map that allocates nothing on the steady path: a snapshot is the
// only place that builds anything.
const (
	// budgetDefaultBurst is the token capacity when the setting is unset/zero.
	budgetDefaultBurst = 5
	// budgetMaxWait caps one wait. A queued host keeps its debt (tokens go
	// negative), so requests degrade into a slower pace instead of hanging a
	// shell for the whole queue.
	budgetMaxWait = 2 * time.Second
	// budgetCooldown is the fixed pause a burned host gets: long enough for a
	// WAF/ratelimiter to forget us, short enough that an idle worker resumes.
	budgetCooldown = 60 * time.Second
	// budgetPenalty is how long a burn keeps the host's rate halved.
	budgetPenalty = 10 * time.Minute
	// budgetMaxHalvings bounds recovery debt ("halve again at most twice").
	budgetMaxHalvings = 2
	// budgetIdle is how long an untouched host is kept for the snapshot.
	budgetIdle = 10 * time.Minute
	// maxBudgetHosts bounds the map; a wide scan can otherwise grow it without
	// limit for the lifetime of the process.
	maxBudgetHosts = 512
)

// bucket is one host's token bucket plus its observed counters. All fields are
// guarded by budget.mu.
type bucket struct {
	tokens        float64
	last          time.Time
	requests      int64
	s429          int64
	s403          int64
	streak403     int
	lastStatus    int
	halvings      int
	cooldownUntil time.Time
	penaltyUntil  time.Time
}

// budget is the per-host egress limiter. The zero value is "off", which is what a
// Traffic built in a test gets.
type budget struct {
	mu    sync.Mutex
	hosts map[string]*bucket
	rps   float64 // global sustained rate per host
	burst int
	// on mirrors rps > 0 so BudgetOn and the addon's hook stay lock-free.
	on atomic.Bool
	// now is the clock seam for tests; nil means time.Now.
	now func() time.Time
}

func (b *budget) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// burstValue is the effective capacity (default when unset). Callers hold mu.
func (b *budget) burstValue() int {
	if b.burst <= 0 {
		return budgetDefaultBurst
	}
	return b.burst
}

// rateLocked is the host's effective rate: the global rate, halved per burn while
// the penalty lasts. Callers hold mu.
func (b *budget) rateLocked(bk *bucket, now time.Time) float64 {
	if bk.halvings > 0 && now.Before(bk.penaltyUntil) {
		return b.rps / float64(int(1)<<bk.halvings)
	}
	return b.rps
}

// bucketLocked returns the host's bucket, creating it with a full burst. Callers
// hold mu.
func (b *budget) bucketLocked(host string, now time.Time) *bucket {
	if bk, ok := b.hosts[host]; ok {
		return bk
	}
	if b.hosts == nil {
		b.hosts = make(map[string]*bucket, 16)
	}
	if len(b.hosts) >= maxBudgetHosts {
		b.pruneLocked(now)
	}
	bk := &bucket{tokens: float64(b.burstValue()), last: now}
	b.hosts[host] = bk
	return bk
}

// pruneLocked drops idle hosts until there is room again. Callers hold mu.
func (b *budget) pruneLocked(now time.Time) {
	for h, bk := range b.hosts {
		if now.After(bk.cooldownUntil) && now.Sub(bk.last) > budgetIdle {
			delete(b.hosts, h)
		}
	}
	// Still full: a live fleet wider than the cap. Drop anything not currently
	// cooling down; its counters are advisory only and rebuild on the next hit.
	if len(b.hosts) >= maxBudgetHosts {
		for h, bk := range b.hosts {
			if len(b.hosts) < maxBudgetHosts {
				break
			}
			if !now.Before(bk.cooldownUntil) {
				delete(b.hosts, h)
			}
		}
	}
	// Every entry is cooling down. Cooldowns are the one state worth keeping, but
	// so is the bound: give up arbitrary entries rather than grow without limit.
	for h := range b.hosts {
		if len(b.hosts) < maxBudgetHosts {
			break
		}
		delete(b.hosts, h)
	}
}

// take accounts one request against host and returns how long the caller must wait
// before forwarding it: the rest of a cooldown, or the time the next token needs.
// The result is capped at budgetMaxWait so a long queue degrades instead of
// hanging, and the token debt is kept so the pace still settles.
func (b *budget) take(host string, now time.Time) time.Duration {
	if host == "" || !b.on.Load() {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.on.Load() { // disabled while we waited for the lock
		return 0
	}
	bk := b.bucketLocked(host, now)
	burst := float64(b.burstValue())
	// Refill first: elapsed time at the effective rate, capped at the burst.
	if elapsed := now.Sub(bk.last); elapsed > 0 {
		bk.tokens = math.Min(burst, bk.tokens+elapsed.Seconds()*b.rateLocked(bk, now))
	}
	if bk.tokens > burst { // a lowered burst clamps existing credit
		bk.tokens = burst
	}
	bk.last = now
	bk.requests++

	var wait time.Duration
	// A cooling host still sends the request out after the capped wait instead of
	// holding it for the whole cooldown: during a burn that paces the host at one
	// request per budgetMaxWait, and no shell ever stalls a full minute. The
	// tokens it would have spent are kept, so the rate resumes intact afterwards.
	if now.Before(bk.cooldownUntil) {
		wait = bk.cooldownUntil.Sub(now)
	} else if bk.tokens >= 1 {
		bk.tokens--
	} else if rate := b.rateLocked(bk, now); rate > 0 {
		wait = time.Duration((1 - bk.tokens) / rate * float64(time.Second))
		bk.tokens-- // debt: the next request waits a step longer
	}
	if wait > budgetMaxWait {
		wait = budgetMaxWait
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// observe folds one response status into the host's counters and applies adaptive
// backoff: a 429, or the third consecutive 403, burns the host (fixed cooldown,
// halved rate for ten minutes). Any 2xx/3xx clears the 403 streak.
func (b *budget) observe(host string, status int, now time.Time) {
	if host == "" || status <= 0 || !b.on.Load() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.on.Load() {
		return
	}
	bk := b.bucketLocked(host, now)
	// The response lands later than the request; keep the refill clock honest by
	// crediting the gap here before moving it, or that time would be lost.
	if elapsed := now.Sub(bk.last); elapsed > 0 {
		bk.tokens = math.Min(float64(b.burstValue()), bk.tokens+elapsed.Seconds()*b.rateLocked(bk, now))
	}
	bk.last = now
	bk.lastStatus = status
	switch {
	case status == 429:
		bk.s429++
		b.burnLocked(bk, now)
	case status == 403:
		bk.s403++
		bk.streak403++
		if bk.streak403 >= 3 {
			b.burnLocked(bk, now)
		}
	case status < 400:
		bk.streak403 = 0
	}
}

// burnLocked starts the cooldown and halves the host rate. Callers hold mu.
func (b *budget) burnLocked(bk *bucket, now time.Time) {
	bk.cooldownUntil = now.Add(budgetCooldown)
	if bk.halvings < budgetMaxHalvings {
		bk.halvings++
	}
	bk.penaltyUntil = now.Add(budgetPenalty)
}

// HostBudget is one host's budget state, for status surfaces (docs §12). Rate is
// the effective rate after any burns; CooldownUntil is unix seconds, 0 when clear.
type HostBudget struct {
	Host          string  `json:"host"`
	Requests      int64   `json:"requests"`
	S429          int64   `json:"s429"`
	S403          int64   `json:"s403"`
	Rate          float64 `json:"rate"`
	LastStatus    int     `json:"last_status"`
	CooldownUntil int64   `json:"cooldown_until"`
}

// SetBudget installs the per-host budget. rps <= 0 (or NaN/Inf) disables the
// limiter and clears every counter. burst <= 0 falls back to the default.
func (t *Traffic) SetBudget(rps float64, burst int) { t.budget.set(rps, burst) }

// set is the implementation, reachable without a Traffic for tests.
func (b *budget) set(rps float64, burst int) {
	if math.IsNaN(rps) || math.IsInf(rps, 0) || rps <= 0 {
		b.mu.Lock()
		b.rps, b.burst, b.hosts = 0, 0, nil
		b.mu.Unlock()
		b.on.Store(false)
		return
	}
	if burst <= 0 {
		burst = budgetDefaultBurst
	}
	b.mu.Lock()
	b.rps, b.burst = rps, burst
	// Keep the counters across a rate change, but clamp credit to the new burst.
	for _, bk := range b.hosts {
		if max := float64(burst); bk.tokens > max {
			bk.tokens = max
		}
	}
	b.mu.Unlock()
	b.on.Store(true)
}

// BudgetOn reports whether a per-host egress budget is active. With capture off it
// is the only reason to route traffic through the proxy at all.
func (t *Traffic) BudgetOn() bool { return t.budget.on.Load() }

// take waits for host's next token, honouring its cooldown. Called from the
// addon's Request hook, before the request is forwarded.
func (t *Traffic) take(host string) {
	w := t.budget.take(host, t.budget.clock())
	if w > 0 {
		time.Sleep(w)
	}
}

// BudgetSnapshot copies the per-host counters for a status surface. Empty (nil)
// when the budget is off.
func (t *Traffic) BudgetSnapshot() map[string]HostBudget {
	return t.budget.snapshot(t.budget.clock())
}

func (b *budget) snapshot(now time.Time) map[string]HostBudget {
	if !b.on.Load() {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.on.Load() { // disabled while we waited for the lock
		return nil
	}
	out := make(map[string]HostBudget, len(b.hosts))
	for h, bk := range b.hosts {
		hb := HostBudget{
			Host:       h,
			Requests:   bk.requests,
			S429:       bk.s429,
			S403:       bk.s403,
			Rate:       b.rateLocked(bk, now),
			LastStatus: bk.lastStatus,
		}
		if now.Before(bk.cooldownUntil) {
			hb.CooldownUntil = bk.cooldownUntil.Unix()
		}
		out[h] = hb
	}
	return out
}
