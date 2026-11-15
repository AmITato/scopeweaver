package db

import (
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/locale"
)

// Host leases keep N workers from converging on the same host: a worker must own
// every host an intent touches before it may claim the intent. Rows carry a TTL so
// a crashed worker cannot lock a host forever. See docs/opsec-blend.md §7.4.
//
// host is a lease key, not necessarily a bare hostname: a host with concurrency N
// uses keys "host#1".."host#N" so each slot is claimed independently. The bare
// host is the single slot. Keys are normalised here, so callers may pass either.
const (
	// DefaultHostLeaseTTL applies when a caller passes a non-positive TTL.
	DefaultHostLeaseTTL = 2 * time.Minute
	// MaxHostLeaseTTL caps a caller-supplied TTL.
	MaxHostLeaseTTL = 30 * time.Minute
	// MaxHostConcurrency caps tasks.stealth.host_concurrency.
	MaxHostConcurrency = 8
)

// normalizeLeaseHost lowercases a lease key and drops one trailing DNS dot.
func normalizeLeaseHost(host string) string {
	return DomainKey(host)
}

func leaseTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultHostLeaseTTL
	}
	if ttl > MaxHostLeaseTTL {
		return MaxHostLeaseTTL
	}
	return ttl
}

// TryLeaseHost atomically takes the lease on host for owner and reports whether
// owner now holds it. An unexpired lease held by another owner denies the call;
// an expired one is taken over, and a lease the caller already owns is refreshed
// in place. The single upsert statement serialises concurrent claimants.
func (d *DB) TryLeaseHost(taskID int64, host, owner string, ttl time.Duration) (bool, error) {
	if taskID <= 0 {
		return false, locale.Errorf("host lease requires task context")
	}
	host = normalizeLeaseHost(host)
	owner = strings.TrimSpace(owner)
	if host == "" {
		return false, locale.Errorf("host lease requires a host")
	}
	if owner == "" {
		return false, locale.Errorf("host lease requires an owner")
	}
	var one int
	err := d.QueryRow(`INSERT INTO task_host_leases(task_id, host, owner, expires_at)
VALUES ($1, $2, $3, now() + make_interval(secs => $4::float8))
ON CONFLICT (task_id, host) DO UPDATE
SET owner = EXCLUDED.owner, expires_at = EXCLUDED.expires_at
WHERE task_host_leases.expires_at <= now() OR task_host_leases.owner = EXCLUDED.owner
RETURNING 1`,
		taskID, host, owner, leaseTTL(ttl).Seconds()).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseHost drops a lease only when owner still holds it, so a worker whose
// lease was reaped and taken over cannot release the new holder's row.
func (d *DB) ReleaseHost(taskID int64, host, owner string) error {
	if taskID <= 0 {
		return locale.Errorf("host lease requires task context")
	}
	host = normalizeLeaseHost(host)
	owner = strings.TrimSpace(owner)
	if host == "" || owner == "" {
		return locale.Errorf("host lease requires host and owner")
	}
	_, err := d.Exec(`DELETE FROM task_host_leases
WHERE task_id=$1 AND host=$2 AND owner=$3`, taskID, host, owner)
	return err
}

// ReapHostLeases drops every expired lease of a task (boot, beside
// ResetRunningIntents) and returns how many rows went away.
func (d *DB) ReapHostLeases(taskID int64) (int64, error) {
	res, err := d.Exec(`DELETE FROM task_host_leases WHERE task_id=$1 AND expires_at <= now()`, taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// HostLeaseOwner reports the live owner of a lease for diagnostics. The bool is
// false when the lease is absent or already expired.
func (d *DB) HostLeaseOwner(taskID int64, host string) (string, bool, error) {
	if taskID <= 0 {
		return "", false, nil
	}
	host = normalizeLeaseHost(host)
	if host == "" {
		return "", false, nil
	}
	var owner string
	err := d.QueryRow(`SELECT owner FROM task_host_leases
WHERE task_id=$1 AND host=$2 AND expires_at > now()`, taskID, host).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return owner, true, nil
}

// TaskHostConcurrency is how many workers may share one host: 0 (unlimited) for
// pentest tasks, otherwise tasks.stealth.host_concurrency clamped to 1..8 with
// 0, absent or unparsable input meaning 1. Read failures fail open to the
// historical profile, mirroring TaskOpsec.
func (d *DB) TaskHostConcurrency(taskID int64) int {
	if taskID <= 0 {
		return 0
	}
	var mode string
	var raw sql.NullString
	err := d.QueryRow(`SELECT COALESCE(mode,'pentest'), stealth->>'host_concurrency'
FROM tasks WHERE id=$1`, taskID).Scan(&mode, &raw)
	if err != nil {
		return 0
	}
	if normalized, ok := NormalizeTaskMode(mode); !ok || normalized != TaskModeOp {
		return 0
	}
	return parseHostConcurrency(raw.String)
}

func parseHostConcurrency(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		f, ferr := strconv.ParseFloat(raw, 64)
		if ferr != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 1
		}
		n = int(math.Round(f))
	}
	if n <= 0 {
		return 1
	}
	if n > MaxHostConcurrency {
		return MaxHostConcurrency
	}
	return n
}

// AssetStore delegations keep the tool layer on the store it already holds.
func (s *AssetStore) TryLeaseHost(taskID int64, host, owner string, ttl time.Duration) (bool, error) {
	return s.db.TryLeaseHost(taskID, host, owner, ttl)
}
func (s *AssetStore) ReleaseHost(taskID int64, host, owner string) error {
	return s.db.ReleaseHost(taskID, host, owner)
}
func (s *AssetStore) ReapHostLeases(taskID int64) (int64, error) {
	return s.db.ReapHostLeases(taskID)
}
func (s *AssetStore) HostLeaseOwner(taskID int64, host string) (string, bool, error) {
	return s.db.HostLeaseOwner(taskID, host)
}
func (s *AssetStore) TaskHostConcurrency(taskID int64) int {
	return s.db.TaskHostConcurrency(taskID)
}
