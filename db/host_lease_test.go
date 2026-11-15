package db

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/internal/dbtest"
)

func leaseTask(t *testing.T, d *DB, mode string, stealth string) *Task {
	t.Helper()
	opts := TaskCreateOptions{Mode: &mode}
	if stealth != "" {
		opts.Stealth = json.RawMessage(stealth)
	}
	task, err := d.CreateTaskWithOptions("host lease fixture", "goal", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	return task
}

func TestHostLeaseAcquireRelease(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	ok, err := d.TryLeaseHost(task.ID, "example.com", "worker-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	owner, held, err := d.HostLeaseOwner(task.ID, "example.com")
	if err != nil || !held || owner != "worker-a" {
		t.Fatalf("owner = %q, %v, %v", owner, held, err)
	}
	if err := d.ReleaseHost(task.ID, "example.com", "worker-a"); err != nil {
		t.Fatal(err)
	}
	if _, held, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || held {
		t.Fatalf("lease survived release: held=%v err=%v", held, err)
	}
	ok, err = d.TryLeaseHost(task.ID, "example.com", "worker-b", time.Minute)
	if err != nil || !ok {
		t.Fatalf("re-acquire after release = %v, %v", ok, err)
	}
}

func TestHostLeaseSameOwnerRefresh(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	if ok, err := d.TryLeaseHost(task.ID, "example.com", "worker-a", time.Minute); err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	// Re-acquiring a live lease you already hold succeeds and pushes the expiry out.
	if ok, err := d.TryLeaseHost(task.ID, "example.com", "worker-a", time.Minute); err != nil || !ok {
		t.Fatalf("same-owner refresh = %v, %v", ok, err)
	}
	var remaining float64
	if err := d.QueryRow(`SELECT EXTRACT(EPOCH FROM expires_at - now()) FROM task_host_leases
WHERE task_id=$1 AND host=$2`, task.ID, "example.com").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining < 50 {
		t.Fatalf("refresh did not extend expiry: %.0fs", remaining)
	}
}

func TestHostLeaseWrongOwnerRelease(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	if ok, err := d.TryLeaseHost(task.ID, "example.com", "worker-a", time.Minute); err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	if err := d.ReleaseHost(task.ID, "example.com", "worker-b"); err != nil {
		t.Fatal(err)
	}
	if owner, held, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || !held || owner != "worker-a" {
		t.Fatalf("wrong-owner release dropped the lease: %q %v %v", owner, held, err)
	}
}

func TestHostLeaseExpiredTakeover(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	if ok, err := d.TryLeaseHost(task.ID, "example.com", "worker-a", time.Minute); err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	if _, err := d.Exec(`UPDATE task_host_leases SET expires_at = now() - interval '1 second'
WHERE task_id=$1 AND host=$2`, task.ID, "example.com"); err != nil {
		t.Fatal(err)
	}
	if owner, held, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || held || owner != "" {
		t.Fatalf("expired lease still reported live: %q %v %v", owner, held, err)
	}
	ok, err := d.TryLeaseHost(task.ID, "example.com", "worker-b", time.Minute)
	if err != nil || !ok {
		t.Fatalf("takeover = %v, %v", ok, err)
	}
	if owner, _, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || owner != "worker-b" {
		t.Fatalf("takeover owner = %q, %v", owner, err)
	}
	// The previous owner can no longer release the new holder's row.
	if err := d.ReleaseHost(task.ID, "example.com", "worker-a"); err != nil {
		t.Fatal(err)
	}
	if _, held, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || !held {
		t.Fatalf("stale owner released the takeover: held=%v err=%v", held, err)
	}
}

func TestHostLeaseLiveContention(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	const owners = 8
	start := make(chan struct{})
	results := make([]bool, owners)
	errs := make([]error, owners)
	var wg sync.WaitGroup
	for i := 0; i < owners; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = d.TryLeaseHost(task.ID, "example.com", "worker-"+itoa(int64(i)), time.Minute)
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("worker-%d: %v", i, errs[i])
		}
		if results[i] {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1 (results %v)", wins, results)
	}
	if _, held, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || !held {
		t.Fatalf("winner did not retain the lease: held=%v err=%v", held, err)
	}
}

func TestHostLeaseNormalisation(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	if ok, err := d.TryLeaseHost(task.ID, "  Example.COM. ", " worker-a ", time.Minute); err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	if owner, held, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || !held || owner != "worker-a" {
		t.Fatalf("normalised lookup = %q %v %v", owner, held, err)
	}
	if ok, err := d.TryLeaseHost(task.ID, "EXAMPLE.com.", "worker-b", time.Minute); err != nil || ok {
		t.Fatalf("variant spelling took a live lease: %v, %v", ok, err)
	}
	var rows int
	if err := d.QueryRow(`SELECT count(*) FROM task_host_leases WHERE task_id=$1`, task.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	if err := d.ReleaseHost(task.ID, "eXaMpLe.CoM.", "worker-a"); err != nil {
		t.Fatal(err)
	}
	if _, held, err := d.HostLeaseOwner(task.ID, "example.com"); err != nil || held {
		t.Fatalf("release by variant spelling failed: held=%v err=%v", held, err)
	}
}

func TestHostLeaseTaskIsolation(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	a := leaseTask(t, d, TaskModeOp, "")
	b := leaseTask(t, d, TaskModeOp, "")

	if ok, err := d.TryLeaseHost(a.ID, "example.com", "worker-a", time.Minute); err != nil || !ok {
		t.Fatalf("task a acquire = %v, %v", ok, err)
	}
	if ok, err := d.TryLeaseHost(b.ID, "example.com", "worker-b", time.Minute); err != nil || !ok {
		t.Fatalf("task b acquire = %v, %v", ok, err)
	}
	if err := d.ReleaseHost(a.ID, "example.com", "worker-a"); err != nil {
		t.Fatal(err)
	}
	if _, held, err := d.HostLeaseOwner(b.ID, "example.com"); err != nil || !held {
		t.Fatalf("releasing task a touched task b: held=%v err=%v", held, err)
	}
}

func TestHostLeaseReap(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	if ok, err := d.TryLeaseHost(task.ID, "expired.example", "worker-a", time.Minute); err != nil || !ok {
		t.Fatalf("acquire expired = %v, %v", ok, err)
	}
	if ok, err := d.TryLeaseHost(task.ID, "live.example", "worker-a", time.Minute); err != nil || !ok {
		t.Fatalf("acquire live = %v, %v", ok, err)
	}
	if _, err := d.Exec(`UPDATE task_host_leases SET expires_at = now() - interval '1 minute'
WHERE task_id=$1 AND host=$2`, task.ID, "expired.example"); err != nil {
		t.Fatal(err)
	}
	n, err := d.ReapHostLeases(task.ID)
	if err != nil || n != 1 {
		t.Fatalf("reap = %d, %v", n, err)
	}
	if _, held, err := d.HostLeaseOwner(task.ID, "live.example"); err != nil || !held {
		t.Fatalf("reap dropped a live lease: held=%v err=%v", held, err)
	}
}

func TestHostLeaseValidation(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task := leaseTask(t, d, TaskModeOp, "")

	if _, err := d.TryLeaseHost(0, "example.com", "worker-a", time.Minute); err == nil {
		t.Fatal("missing task must fail")
	}
	if _, err := d.TryLeaseHost(task.ID, "   ", "worker-a", time.Minute); err == nil {
		t.Fatal("empty host must fail")
	}
	if _, err := d.TryLeaseHost(task.ID, "example.com", "  ", time.Minute); err == nil {
		t.Fatal("empty owner must fail")
	}
	if err := d.ReleaseHost(task.ID, "example.com", ""); err == nil {
		t.Fatal("empty release owner must fail")
	}
	// A non-positive TTL still takes a lease with the default lifetime.
	if ok, err := d.TryLeaseHost(task.ID, "example.com", "worker-a", 0); err != nil || !ok {
		t.Fatalf("default ttl acquire = %v, %v", ok, err)
	}
}

func TestHostLeaseConcurrency(t *testing.T) {
	dsn := dbtest.DSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()

	pentest := leaseTask(t, d, TaskModePentest, `{"host_concurrency":4}`)
	if got := d.TaskHostConcurrency(pentest.ID); got != 0 {
		t.Fatalf("pentest concurrency = %d, want 0 (unlimited)", got)
	}

	cases := []struct {
		stealth string
		want    int
	}{
		{`{}`, 1},
		{`{"host_concurrency":null}`, 1},
		{`{"host_concurrency":0}`, 1},
		{`{"host_concurrency":-3}`, 1},
		{`{"host_concurrency":3}`, 3},
		{`{"host_concurrency":8}`, 8},
		{`{"host_concurrency":999}`, 8},
		{`{"host_concurrency":2.6}`, 3},
		{`{"host_concurrency":"many"}`, 1},
		{`{"host_concurrency":true}`, 1},
		{`{"host_concurrency":{}}`, 1},
		{`{"rps":1,"burst":5}`, 1},
	}
	for _, tc := range cases {
		task := leaseTask(t, d, TaskModeOp, tc.stealth)
		if got := d.TaskHostConcurrency(task.ID); got != tc.want {
			t.Fatalf("TaskHostConcurrency(%s) = %d, want %d", tc.stealth, got, tc.want)
		}
	}

	// Unknown task fails open to the pentest profile.
	if got := d.TaskHostConcurrency(1 << 62); got != 0 {
		t.Fatalf("missing task concurrency = %d, want 0", got)
	}
}
