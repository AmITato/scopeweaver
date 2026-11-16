package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/egress"
	"github.com/Autumn-27/artex/traffic"
)

// The Now board is a read-only operator surface (docs/opsec-blend.md §12). It
// answers "what is happening right now" with a small, bounded number of reads:
// tasks, their running intents, one asset lookup, live leases and open residue.
// No write path lives here, and no new table backs it.
const (
	nowTaskLimit = 50
	nowHostLimit = 20
)

// rotatingReporter is the optional capability a rotation implementation may add.
// Rotation belongs to another slice of the pipeline, so it is deliberately not
// part of egress.Provider: the board type-asserts for it and reports false until
// that lands.
type rotatingReporter interface {
	Rotating() bool
}

// nowTaskRow is the small task projection the board needs.
type nowTaskRow struct {
	ID     int64
	Name   string
	Mode   string
	Phase  string
	Status string
}

// nowInput is everything buildNowBoard renders. Keeping collection and rendering
// apart lets the mapping be tested without a proxy, a database or an egress CLI.
type nowInput struct {
	egress   egress.Status
	rotating bool

	rps    float64
	burst  int
	budget map[string]traffic.HostBudget

	tasks       []nowTaskRow
	workers     map[string]int           // task id -> live engine executions
	hostWorkers map[int64]map[string]int // task id -> host -> running intents
	leases      map[int64]map[string]int // task id -> host -> held leases
	residue     map[int64]int            // task id -> open obvious residue
}

// nowBoard serves GET /api/now. Authentication is requireAuth, like /api/tasks.
func (s *Server) nowBoard(w http.ResponseWriter, r *http.Request) {
	in, err := s.collectNow(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, buildNowBoard(in))
}

// collectNow reads each concern once. The board is polled, so tasks and hosts are
// capped (nowTaskLimit/nowHostLimit) before anything is rendered.
func (s *Server) collectNow(ctx context.Context) (nowInput, error) {
	in := nowInput{
		budget:      map[string]traffic.HostBudget{},
		workers:     map[string]int{},
		hostWorkers: map[int64]map[string]int{},
		leases:      map[int64]map[string]int{},
		residue:     map[int64]int{},
	}

	// Egress status is a local CLI read, not a database concern. A driver error
	// still returns a driver name; the safety fields then tell the truth.
	st, _ := s.m.EgressStatus(ctx)
	if strings.TrimSpace(st.Driver) == "" {
		st.Driver = s.m.EgressDriver()
	}
	in.egress = st
	in.rotating = s.egressRotating()

	// Configured rate/burst plus the in-memory per-host counters.
	in.rps, in.burst = s.m.EgressBudget()
	if tr := s.m.Traffic(); tr != nil {
		if snap := tr.BudgetSnapshot(); snap != nil {
			in.budget = snap
		}
	}

	pg := s.m.PG()
	if pg == nil {
		return in, nil
	}
	tasks, err := nowTasks(pg, nowTaskLimit)
	if err != nil {
		return in, err
	}
	in.tasks = tasks
	if len(tasks) == 0 {
		return in, nil
	}
	taskIDs := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}

	// Running intents carry the asset ids that anchor each worker to a host.
	intentAssets, err := nowRunningIntentAssets(pg, taskIDs)
	if err != nil {
		return in, err
	}
	var assetIDs []int64
	seenAsset := map[int64]bool{}
	for _, groups := range intentAssets {
		for _, ids := range groups {
			for _, id := range ids {
				if id > 0 && !seenAsset[id] {
					seenAsset[id] = true
					assetIDs = append(assetIDs, id)
				}
			}
		}
	}
	byID := map[int64]*db.Asset{}
	if as := s.m.Assets(); as != nil && len(assetIDs) > 0 {
		assets, err := as.GetByIDs(assetIDs)
		if err != nil {
			return in, err
		}
		for _, a := range assets {
			if a != nil {
				byID[a.ID] = a
			}
		}
	}
	for taskID, groups := range intentAssets {
		perHost := map[string]int{}
		for _, ids := range groups {
			for _, host := range intentHostsFromIDs(ids, byID) {
				host = db.DomainKey(host)
				if host != "" {
					perHost[host]++
				}
			}
		}
		in.hostWorkers[taskID] = perHost
	}

	leases, err := nowHostLeases(pg, taskIDs)
	if err != nil {
		return in, err
	}
	in.leases = leases
	residue, err := nowResidueCounts(pg, taskIDs)
	if err != nil {
		return in, err
	}
	in.residue = residue

	if s.engine != nil {
		in.workers = s.engine.ActiveWorkersPerTask()
	}
	return in, nil
}

// egressRotating reads the optional rotation flag from either the provider or the
// manager, so this compiles and reports false before rotation lands.
func (s *Server) egressRotating() bool {
	if r, ok := s.m.Egress().(rotatingReporter); ok && r.Rotating() {
		return true
	}
	if r, ok := any(s.m).(rotatingReporter); ok {
		return r.Rotating()
	}
	return false
}

// buildNowBoard renders the DTO. Collections are always non-nil.
func buildNowBoard(in nowInput) NowBoardDTO {
	driver := strings.TrimSpace(in.egress.Driver)
	if driver == "" {
		driver = "none"
	}
	burst := in.burst
	if burst <= 0 {
		burst = defaultEgressBurst
	}

	// Budget counters are per host and global to the process. Index them by the
	// same normalised host key the task rollup uses.
	budgetByHost := make(map[string]traffic.HostBudget, len(in.budget))
	for host, hb := range in.budget {
		key := db.DomainKey(host)
		if key == "" {
			key = host
		}
		budgetByHost[key] = hb
	}

	board := NowBoardDTO{
		Egress: NowEgressDTO{
			Driver:    driver,
			Managed:   driver != "none",
			Connected: in.egress.Connected,
			Lockdown:  in.egress.Lockdown,
			ExitIP:    in.egress.ExitIP,
			City:      in.egress.City,
			Country:   in.egress.Country,
			Safe:      in.egress.Safe(),
			Rotating:  in.rotating,
		},
		Budget: NowBudgetDTO{
			RPS:   in.rps,
			Burst: burst,
			Hosts: nowBudgetHosts(in.budget),
		},
		Tasks: make([]NowTaskDTO, 0, len(in.tasks)),
	}
	for _, row := range in.tasks {
		union := map[string]int{}
		for host, workers := range in.hostWorkers[row.ID] {
			union[host] = workers
		}
		for host := range in.leases[row.ID] {
			if _, ok := union[host]; !ok {
				union[host] = 0
			}
		}
		board.Tasks = append(board.Tasks, NowTaskDTO{
			ID:          strconv.FormatInt(row.ID, 10),
			Name:        row.Name,
			Mode:        row.Mode,
			Phase:       row.Phase,
			Status:      row.Status,
			Workers:     in.workers[strconv.FormatInt(row.ID, 10)],
			ResidueOpen: in.residue[row.ID],
			Hosts:       nowTaskHosts(union, in.leases[row.ID], budgetByHost),
		})
	}
	return board
}

// nowBudgetHosts renders the process-wide budget snapshot deterministically.
func nowBudgetHosts(snapshot map[string]traffic.HostBudget) []NowBudgetHostDTO {
	out := make([]NowBudgetHostDTO, 0, len(snapshot))
	for host, hb := range snapshot {
		if hb.Host == "" {
			hb.Host = host
		}
		out = append(out, NowBudgetHostDTO{
			Host:          hb.Host,
			Requests:      hb.Requests,
			S429:          hb.S429,
			S403:          hb.S403,
			LastStatus:    hb.LastStatus,
			CooldownUntil: hb.CooldownUntil,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Host < out[j].Host
	})
	return out
}

// nowTaskHosts rolls one task's running intents and leases into host rows, adds
// the shared budget counters, then sorts and caps them so a poll stays cheap.
func nowTaskHosts(workers, leases map[string]int, budget map[string]traffic.HostBudget) []NowTaskHostDTO {
	out := make([]NowTaskHostDTO, 0, len(workers)+len(leases))
	for host, n := range workers {
		row := NowTaskHostDTO{Host: host, Workers: n, Leases: leases[host]}
		if hb, ok := budget[host]; ok {
			row.Requests, row.S429, row.CooldownUntil = hb.Requests, hb.S429, hb.CooldownUntil
		}
		out = append(out, row)
	}
	for host, n := range leases {
		if _, ok := workers[host]; ok {
			continue
		}
		row := NowTaskHostDTO{Host: host, Leases: n}
		if hb, ok := budget[host]; ok {
			row.Requests, row.S429, row.CooldownUntil = hb.Requests, hb.S429, hb.CooldownUntil
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		if out[i].Workers != out[j].Workers {
			return out[i].Workers > out[j].Workers
		}
		return out[i].Host < out[j].Host
	})
	if len(out) > nowHostLimit {
		out = out[:nowHostLimit]
	}
	return out
}

// nowTasks lists alive tasks newest first. Phase is not on db.Task, so it is read
// here in the same query rather than with a per-task follow-up.
func nowTasks(pg *db.DB, limit int) ([]nowTaskRow, error) {
	rows, err := pg.Query(`SELECT id, COALESCE(name,''), COALESCE(mode,'pentest'),
COALESCE(phase,'active'), status
FROM tasks WHERE deleted_at IS NULL
ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nowTaskRow
	for rows.Next() {
		var t nowTaskRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Mode, &t.Phase, &t.Status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// nowRunningIntentAssets groups each live task's running intents by their
// anchored asset ids.
func nowRunningIntentAssets(pg *db.DB, taskIDs []int64) (map[int64][][]int64, error) {
	out := map[int64][][]int64{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := pg.Query(`SELECT t.id, n.payload
FROM tasks t
JOIN exploration_nodes n ON n.exploration_id=t.exploration_id
WHERE t.id = ANY($1::bigint[]) AND n.kind='intent' AND n.state='running'
ORDER BY t.id, n.id`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int64
		var payload []byte
		if err := rows.Scan(&taskID, &payload); err != nil {
			return nil, err
		}
		out[taskID] = append(out[taskID], intentAssetIDs(&db.Node{Payload: json.RawMessage(payload)}))
	}
	return out, rows.Err()
}

// nowHostLeases counts unexpired lease rows per task and bare host. A host with
// concurrency N stores host#1..host#N, so the slot suffix is folded back in.
func nowHostLeases(pg *db.DB, taskIDs []int64) (map[int64]map[string]int, error) {
	out := map[int64]map[string]int{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := pg.Query(`SELECT task_id, host, count(*)
FROM task_host_leases
WHERE task_id = ANY($1::bigint[]) AND expires_at > now()
GROUP BY task_id, host`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int64
		var key string
		var n int
		if err := rows.Scan(&taskID, &key, &n); err != nil {
			return nil, err
		}
		if i := strings.IndexByte(key, '#'); i >= 0 {
			key = key[:i]
		}
		host := db.DomainKey(key)
		if host == "" {
			continue
		}
		if out[taskID] == nil {
			out[taskID] = map[string]int{}
		}
		out[taskID][host] += n
	}
	return out, rows.Err()
}

// nowResidueCounts counts open, removable, obvious residue per task — the same
// rows db.CountOpenObviousResidue reports, in one aggregate query.
func nowResidueCounts(pg *db.DB, taskIDs []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := pg.Query(`SELECT task_id, count(*)
FROM task_residue
WHERE task_id = ANY($1::bigint[]) AND removed_at IS NULL AND removable AND rank=$2
GROUP BY task_id`, taskIDs, db.ResidueRankObvious)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int64
		var n int
		if err := rows.Scan(&taskID, &n); err != nil {
			return nil, err
		}
		out[taskID] = n
	}
	return out, rows.Err()
}
