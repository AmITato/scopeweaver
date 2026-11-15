package blend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- GitHub ---------------------------------------------------------------

func TestGitHubRepoHarvestsObservations(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	srv = httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/repos/acme/app/branches", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]string{{"name": "DEV-101-login"}, {"name": "TS-102-cache"}})
	})
	mux.HandleFunc("/repos/acme/app/commits", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{{
			"commit": map[string]any{
				"message": "DEV-103 fix login redirect\n\nlong body",
				"author":  map[string]string{"name": "Min Kim"},
			},
		}})
	})
	mux.HandleFunc("/repos/acme/app/actions/workflows", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"workflows": []map[string]string{
			{"name": "jira check", "path": ".github/workflows/jira-check.yml"},
			{"name": "CI", "path": ".github/workflows/ci.yml"},
		}})
	})
	mux.HandleFunc("/repos/acme/app/contents/.github/workflows", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]string{
			{"name": "jira-check.yml", "path": ".github/workflows/jira-check.yml", "type": "file", "download_url": srv.URL + "/raw/jira-check.yml"},
			{"name": "ci.yml", "path": ".github/workflows/ci.yml", "type": "file", "download_url": srv.URL + "/raw/ci.yml"},
			{"name": "templates", "path": ".github/workflows/templates", "type": "dir"},
		})
	})
	mux.HandleFunc("/raw/jira-check.yml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "if [[ \"$branch\" =~ ^(DEV|TS|MKB)-[0-9]+ ]]; then\n  exit 1\nfi\n")
	})
	mux.HandleFunc("/raw/ci.yml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "on:\n  push:\n    branches: [main]\n")
	})

	src := &GitHubSource{BaseURL: srv.URL, Client: srv.Client()}
	obs, err := src.Repo(context.Background(), "acme", "app")
	if err != nil {
		t.Fatalf("Repo: %v", err)
	}

	values := map[Class][]string{}
	for _, o := range obs {
		values[o.Class] = append(values[o.Class], o.Value)
		if o.Source == "" {
			t.Fatalf("observation %+v has no source", o)
		}
	}
	assertContains(t, values[ClassBranch], "DEV-101-login")
	assertContains(t, values[ClassBranch], "TS-102-cache")
	assertContains(t, values[ClassBranch], "regex:^(DEV|TS|MKB)-[0-9]+")
	assertContains(t, values[ClassCommitSubject], "DEV-103 fix login redirect")
	assertContains(t, values[ClassCommitSubject], "Min Kim")
	assertContains(t, values[ClassWorkflow], "jira-check.yml")
	assertContains(t, values[ClassWorkflow], "ci.yml")
	if has(values[ClassWorkflow], "templates") {
		t.Errorf("directory listed as workflow: %v", values[ClassWorkflow])
	}
	for _, o := range obs {
		if o.Class == ClassBranch && strings.HasPrefix(o.Value, "regex:") &&
			!strings.Contains(o.Source, "jira-check.yml") {
			t.Errorf("regex source = %q, want the workflow file", o.Source)
		}
	}
}

func TestGitHubPaginationCapAndPageLimit(t *testing.T) {
	branchCalls, commitCalls := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/app/branches":
			branchCalls++
			page := r.URL.Query().Get("page")
			items := make([]map[string]string, 0, 100)
			for i := 0; i < 100; i++ {
				items = append(items, map[string]string{"name": fmt.Sprintf("p%s-%03d", page, i)})
			}
			writeJSON(t, w, items)
		case "/repos/acme/app/commits":
			commitCalls++
			page := r.URL.Query().Get("page")
			items := make([]map[string]any, 0, 30)
			for i := 0; i < 30; i++ {
				items = append(items, map[string]any{"commit": map[string]any{
					"message": fmt.Sprintf("c%s-%02d", page, i),
					"author":  map[string]string{"name": "Same Author"},
				}})
			}
			writeJSON(t, w, items)
		case "/repos/acme/app/actions/workflows":
			writeJSON(t, w, map[string]any{"workflows": []any{}})
		default:
			writeJSON(t, w, []any{})
		}
	}))
	defer srv.Close()

	src := &GitHubSource{BaseURL: srv.URL, Client: srv.Client()}
	obs, err := src.Repo(context.Background(), "acme", "app")
	if err != nil {
		t.Fatalf("Repo: %v", err)
	}
	if branchCalls > maxGitHubPages {
		t.Errorf("branch requests = %d, want <= %d", branchCalls, maxGitHubPages)
	}
	if commitCalls != maxGitHubPages {
		t.Errorf("commit requests = %d, want %d", commitCalls, maxGitHubPages)
	}
	branches := 0
	for _, o := range obs {
		if o.Class == ClassBranch {
			branches++
		}
	}
	if branches != MaxObservationsPerClass {
		t.Errorf("branch observations = %d, want cap %d", branches, MaxObservationsPerClass)
	}
}

func TestGitHubStopsOnRateLimit(t *testing.T) {
	commitCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/app/branches":
			w.Header().Set("x-ratelimit-remaining", "0")
			w.WriteHeader(http.StatusForbidden)
		case r.URL.Path == "/repos/acme/app/commits":
			commitCalls++
			writeJSON(t, w, []any{})
		default:
			writeJSON(t, w, []any{})
		}
	}))
	defer srv.Close()

	src := &GitHubSource{BaseURL: srv.URL, Client: srv.Client()}
	if _, err := src.Repo(context.Background(), "acme", "app"); !errors.Is(err, errGitHubRateLimit) {
		t.Fatalf("err = %v, want rate limit", err)
	}
	if commitCalls != 0 {
		t.Errorf("commit endpoint called %d times after rate limit", commitCalls)
	}
}

func TestGitHubOptional404AndToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/repos/acme/app/commits":
			writeJSON(t, w, []any{})
		case "/repos/acme/app/actions/workflows", "/repos/acme/app/contents/.github/workflows":
			http.NotFound(w, r)
		default:
			writeJSON(t, w, []any{})
		}
	}))
	defer srv.Close()

	src := &GitHubSource{BaseURL: srv.URL, Client: srv.Client(), Token: "t0ken"}
	if _, err := src.Repo(context.Background(), "acme", "app"); err != nil {
		t.Fatalf("Repo with absent CI: %v", err)
	}
	if gotAuth != "Bearer t0ken" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestGitHubRepoRequiresOwnerAndRepo(t *testing.T) {
	if _, err := (&GitHubSource{}).Repo(context.Background(), "", "app"); err == nil {
		t.Fatal("want error for empty owner")
	}
}

func TestExtractRegexLiterals(t *testing.T) {
	body := strings.Join([]string{
		"if [[ $ref =~ '^(DEV|TS)-[0-9]+' ]]; then",
		`names=$(echo "$ref" | grep -E "^[A-Z]+-[0-9]+$")`,
		"pattern: \"^feature/.+$\"",
		"if [[ $ref =~ ^[a-z]+-[0-9]+[a-z] ]]; then",
		"if [[ $x =~ ^(unclosed ]]; then",
		"# no marker here",
	}, "\n")
	got := extractRegexLiterals(body)
	want := []string{"^(DEV|TS)-[0-9]+", "^[A-Z]+-[0-9]+$", "^feature/.+$", "^[a-z]+-[0-9]+[a-z]"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("literal %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// --- AWS ------------------------------------------------------------------

type fakeAWS struct {
	calls []string
	out   map[string]string
	errs  map[string]error
}

func (f *fakeAWS) run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) < 2 {
		return nil, errors.New("short args")
	}
	key := args[0] + ":" + args[1]
	f.calls = append(f.calls, strings.Join(args, " "))
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	if body, ok := f.out[key]; ok {
		return []byte(body), nil
	}
	return []byte("{}"), nil
}

func TestHarvestAWSEmitsNamesAndTags(t *testing.T) {
	fake := &fakeAWS{out: map[string]string{
		"iam:list-users":               `{"Users":[{"UserName":"svc-deploy"},{"UserName":"svc-deploy"}]}`,
		"iam:list-roles":               `{"Roles":[{"RoleName":"app-runner"}]}`,
		"iam:list-instance-profiles":   `{"InstanceProfiles":[{"InstanceProfileName":"app-runner"}]}`,
		"ec2:describe-tags":            `{"Tags":[{"Key":"Name","Value":"WEB PROD"},{"Key":"Project","Value":"Billing"},{"Key":"Environment","Value":"prod"},{"Key":"Owner","Value":"jane"}]}`,
		"ec2:describe-security-groups": `{"SecurityGroups":[{"GroupName":"Web Public","Description":"web ingress"},{"GroupName":"Web Public","Description":""}]}`,
	}}
	obs, err := HarvestAWS(context.Background(), fake.run, "123456789012")
	if err != nil {
		t.Fatalf("HarvestAWS: %v", err)
	}
	if len(fake.calls) != awsReads {
		t.Fatalf("calls = %d, want %d", len(fake.calls), awsReads)
	}
	for _, call := range fake.calls {
		if !strings.HasSuffix(call, "--output json") {
			t.Errorf("call %q lacks --output json", call)
		}
	}
	byClass := map[Class][]string{}
	for _, o := range obs {
		byClass[o.Class] = append(byClass[o.Class], o.Value)
		if !strings.Contains(o.Source, "123456789012") {
			t.Errorf("source %q lost the account", o.Source)
		}
	}
	if got := byClass[ClassIAMUser]; len(got) != 1 || got[0] != "svc-deploy" {
		t.Errorf("users = %v", got)
	}
	assertContains(t, byClass[ClassIAMRole], "app-runner")
	assertContains(t, byClass[ClassSGName], "Web Public")
	assertContains(t, byClass[ClassSGDescription], "web ingress")
	assertContains(t, byClass[ClassTagValue], "WEB PROD")
	assertContains(t, byClass[ClassTagValue], "Billing")
	assertContains(t, byClass[ClassTagValue], "prod")
	if has(byClass[ClassTagValue], "jane") {
		t.Error("Owner tag should not be harvested")
	}
}

func TestHarvestAWSToleratesPerCallFailure(t *testing.T) {
	fake := &fakeAWS{
		errs: map[string]error{"iam:list-roles": errors.New("AccessDenied")},
		out: map[string]string{
			"iam:list-users":    `{"Users":[{"UserName":"svc-a"}]}`,
			"ec2:describe-tags": `{"Tags":[{"Key":"Name","Value":"db"}]}`,
		},
	}
	obs, err := HarvestAWS(context.Background(), fake.run, "")
	if err != nil {
		t.Fatalf("partial failure should not abort: %v", err)
	}
	assertContains(t, classValues(obs, ClassIAMUser), "svc-a")
	assertContains(t, classValues(obs, ClassTagValue), "db")
	for _, o := range obs {
		if strings.Contains(o.Source, "::") {
			t.Errorf("source %q has an empty account segment", o.Source)
		}
	}
}

func TestHarvestAWSAllFail(t *testing.T) {
	fake := &fakeAWS{errs: map[string]error{
		"iam:list-users":               errors.New("no creds"),
		"iam:list-roles":               errors.New("no creds"),
		"iam:list-instance-profiles":   errors.New("no creds"),
		"ec2:describe-tags":            errors.New("no creds"),
		"ec2:describe-security-groups": errors.New("no creds"),
	}}
	obs, err := HarvestAWS(context.Background(), fake.run, "acct")
	if err == nil {
		t.Fatal("want error when every read fails")
	}
	if len(obs) != 0 {
		t.Errorf("obs = %v, want none", obs)
	}
}

func TestHarvestAWSBadJSONIsPerCall(t *testing.T) {
	fake := &fakeAWS{out: map[string]string{
		"iam:list-users": `{not json`,
		"iam:list-roles": `{"Roles":[{"RoleName":"still-here"}]}`,
	}}
	obs, err := HarvestAWS(context.Background(), fake.run, "")
	if err != nil {
		t.Fatalf("decode failure should be tolerated: %v", err)
	}
	assertContains(t, classValues(obs, ClassIAMRole), "still-here")
}

func TestHarvestAWSNilRunner(t *testing.T) {
	if _, err := HarvestAWS(context.Background(), nil, ""); err == nil {
		t.Fatal("want error for nil runner")
	}
}

// --- UA and merge ---------------------------------------------------------

func TestHarvestUA(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "curl/8.4.0")
	h.Set("Server", "nginx")
	h.Set("X-Powered-By", "Express")
	h.Add("Server", "nginx")
	obs := HarvestUA(h)
	if len(obs) != 3 {
		t.Fatalf("obs = %v, want 3 deduplicated", obs)
	}
	if obs[0].Class != ClassMarker || obs[0].Source != "http-header:user-agent" {
		t.Errorf("first observation = %+v", obs[0])
	}
	if len(HarvestUA(nil)) != 0 {
		t.Error("nil headers should yield nothing")
	}
}

func TestMergeObservationsStampsAndClones(t *testing.T) {
	before := Default()
	obs := []Observation{
		{Class: ClassBranch, Value: "DEV-1-a", Source: "github:a/b:branches"},
		{Class: ClassBranch, Value: "DEV-2-b", Source: "github:a/b:branches"},
		{Class: ClassWorkflow, Value: "ci.yml", Source: "github:a/b:actions/workflows"},
		{Class: Class("nope"), Value: "ignored"},
		{Class: ClassIAMUser, Value: "  ", Source: "aws:x:iam"},
	}
	got := MergeObservations(before, obs)
	if got == before {
		t.Fatal("expected a clone")
	}
	if !before.Empty() {
		t.Error("input style was mutated")
	}
	if got.Version != StyleVersion {
		t.Errorf("version = %d", got.Version)
	}
	if got.ObservedAt.IsZero() {
		t.Error("ObservedAt not stamped")
	}
	if len(got.Sources) != 2 || got.Sources[0] != "github:a/b:actions/workflows" || got.Sources[1] != "github:a/b:branches" {
		t.Errorf("sources = %v, want sorted unique", got.Sources)
	}
	if cs := got.Classes[ClassBranch]; cs == nil || cs.Samples != 2 || len(cs.Examples) != 2 {
		t.Errorf("branch merge = %+v", cs)
	}
	if _, ok := got.Classes[Class("nope")]; ok {
		t.Error("invalid class was merged")
	}
	if _, ok := got.Classes[ClassIAMUser]; ok {
		t.Error("blank value was merged")
	}
}

func TestMergeObservationsNilAndExistingSources(t *testing.T) {
	got := MergeObservations(nil, []Observation{{Class: ClassIAMRole, Value: "runner", Source: " b "}})
	if got.Classes[ClassIAMRole].Samples != 1 {
		t.Fatalf("classes = %+v", got.Classes)
	}
	if len(got.Sources) != 1 || got.Sources[0] != "b" {
		t.Errorf("sources = %v", got.Sources)
	}
}

func TestMergeConfidenceRisesWithSamples(t *testing.T) {
	cs := &ClassStyle{}
	s := &Style{Classes: map[Class]*ClassStyle{ClassSGName: cs}}
	for i := 0; i < 12; i++ {
		s.Merge(Observation{Class: ClassSGName, Value: "Web Public"})
	}
	if cs.Samples != 12 {
		t.Fatalf("samples = %d", cs.Samples)
	}
	if cs.Confidence < GenerateFloor {
		t.Errorf("confidence = %v, want >= %v", cs.Confidence, GenerateFloor)
	}
}

// --- helpers --------------------------------------------------------------

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	if !has(values, want) {
		t.Errorf("%q not in %v", want, values)
	}
}

func has(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func classValues(obs []Observation, class Class) []string {
	var out []string
	for _, o := range obs {
		if o.Class == class {
			out = append(out, o.Value)
		}
	}
	return out
}
