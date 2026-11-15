package blend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// GitHubSource reads a repository's public shape through the GitHub REST API.
// It performs only GETs. Token is optional; without it the rate limit is low.
type GitHubSource struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

const (
	defaultGitHubBase = "https://api.github.com"
	gitHubUserAgent   = "scopeweaver-blend/1"
	maxGitHubPages    = 3
)

// Repo harvests branch names, workflow filenames, commit subjects/authors and
// the target's own branch regex from owner/repo. Endpoint failures are tolerated
// so a partial harvest still returns; the errors are joined in the result.
func (g *GitHubSource) Repo(ctx context.Context, owner, repo string) ([]Observation, error) {
	if g == nil {
		return nil, errors.New("blend: nil GitHubSource")
	}
	owner, repo = strings.TrimSpace(owner), strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return nil, errors.New("blend: github owner and repo are required")
	}
	client := g.client()
	base := strings.TrimRight(strings.TrimSpace(g.BaseURL), "/")
	if base == "" {
		base = defaultGitHubBase
	}
	repoURL := fmt.Sprintf("%s/repos/%s/%s", base, url.PathEscape(owner), url.PathEscape(repo))
	prefix := "github:" + owner + "/" + repo
	h := &gitHubHarvest{source: g, client: client, ctx: ctx, prefix: prefix, out: newCollector()}

	for page := 1; page <= maxGitHubPages && !h.out.full(ClassBranch) && !h.stopped; page++ {
		var items []struct {
			Name string `json:"name"`
		}
		if !h.read(fmt.Sprintf("%s/branches?per_page=100&page=%d", repoURL, page), &items, false) {
			break
		}
		for _, item := range items {
			h.out.add(ClassBranch, item.Name, prefix+":branches")
		}
		if len(items) < 100 {
			break
		}
	}

	for page := 1; page <= maxGitHubPages && !h.out.full(ClassCommitSubject) && !h.stopped; page++ {
		var items []struct {
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
				} `json:"author"`
			} `json:"commit"`
		}
		if !h.read(fmt.Sprintf("%s/commits?per_page=30&page=%d", repoURL, page), &items, false) {
			break
		}
		for _, item := range items {
			h.out.add(ClassCommitSubject, commitSubject(item.Commit.Message), prefix+":commits")
			h.out.add(ClassCommitSubject, item.Commit.Author.Name, prefix+":commit-authors")
		}
		if len(items) < 30 {
			break
		}
	}

	var workflows struct {
		Workflows []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"workflows"`
	}
	if !h.stopped && h.read(repoURL+"/actions/workflows?per_page=100", &workflows, true) {
		for _, wf := range workflows.Workflows {
			h.out.add(ClassWorkflow, workflowFilename(wf.Path, wf.Name), prefix+":actions/workflows")
		}
	}

	var contents []struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		Type        string `json:"type"`
		DownloadURL string `json:"download_url"`
	}
	if !h.stopped && h.read(repoURL+"/contents/.github/workflows", &contents, true) {
		regexFiles := 0
		for _, entry := range contents {
			name := workflowFilename(entry.Path, entry.Name)
			if entry.Type != "" && entry.Type != "file" {
				continue
			}
			h.out.add(ClassWorkflow, name, prefix+":contents/.github/workflows")
			if regexFiles >= MaxWorkflowRegexFiles || !regexWorkflowName(name) {
				continue
			}
			regexFiles++
			for _, literal := range g.branchRegexes(ctx, client, repoURL, entry.DownloadURL, entry.Name) {
				h.out.add(ClassBranch, "regex:"+literal, prefix+":contents/"+entry.Name+":regex")
			}
		}
	}

	return h.out.obs, errors.Join(h.out.errs...)
}

// gitHubHarvest carries per-run state shared by the endpoint readers.
type gitHubHarvest struct {
	source  *GitHubSource
	client  *http.Client
	ctx     context.Context
	prefix  string
	out     *collector
	stopped bool
}

// read GETs url and decodes a 2xx JSON body into dst. Optional endpoints treat
// 404 as a normal absence. It reports whether dst was populated.
func (h *gitHubHarvest) read(rawURL string, dst any, optional bool) bool {
	body, header, status, err := h.source.get(h.ctx, h.client, rawURL)
	if err != nil {
		h.out.fail(fmt.Errorf("%s: %w", rawURL, err))
		return false
	}
	if rateLimited(status, header) {
		h.stopped = true
		h.out.fail(fmt.Errorf("%s: %w", rawURL, errGitHubRateLimit))
		return false
	}
	switch {
	case status == http.StatusNotFound && optional:
		return false
	case status < 200 || status > 299:
		h.out.fail(fmt.Errorf("%s: status %d", rawURL, status))
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		h.out.fail(fmt.Errorf("%s: decode: %w", rawURL, err))
		return false
	}
	return true
}

// branchRegexes fetches one matching workflow file and returns its branch-name
// regex literals. A fetch or decode failure is recorded and skipped.
func (g *GitHubSource) branchRegexes(ctx context.Context, client *http.Client, repoURL, downloadURL, name string) []string {
	target := strings.TrimSpace(downloadURL)
	if target == "" {
		target = repoURL + "/contents/.github/workflows/" + url.PathEscape(path.Base(name))
	}
	body, header, status, err := g.get(ctx, client, target)
	if err != nil {
		return nil
	}
	if rateLimited(status, header) || status < 200 || status > 299 {
		return nil
	}
	text := string(body)
	var encoded struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if json.Unmarshal(body, &encoded) == nil && strings.EqualFold(encoded.Encoding, "base64") && encoded.Content != "" {
		if raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encoded.Content, "\n", "")); err == nil {
			text = string(raw)
		}
	}
	return extractRegexLiterals(text)
}

// get performs one authenticated GET and returns the body, headers and status.
func (g *GitHubSource) get(ctx context.Context, client *http.Client, rawURL string) ([]byte, http.Header, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", gitHubUserAgent)
	if token := strings.TrimSpace(g.Token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.Header, resp.StatusCode, err
	}
	return body, resp.Header, resp.StatusCode, nil
}

// client returns the configured client, or a short-timeout default.
func (g *GitHubSource) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

var errGitHubRateLimit = errors.New("github rate limit exhausted")

// rateLimited recognises the primary-rate-limit response. A 403 for another
// reason does not stop the harvest.
func rateLimited(status int, header http.Header) bool {
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return false
	}
	return strings.TrimSpace(header.Get("x-ratelimit-remaining")) == "0"
}

// workflowFilename reduces a path to its file name, falling back to the
// workflow's display name.
func workflowFilename(p, name string) string {
	if p != "" {
		return path.Base(strings.ReplaceAll(p, "\\", "/"))
	}
	return strings.TrimSpace(name)
}

// regexWorkflowName reports whether a CI file is likely to carry the branch
// grammar.
func regexWorkflowName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "jira") || strings.Contains(lower, "branch")
}

// commitSubject is the first line of a commit message.
func commitSubject(message string) string {
	if line, _, found := strings.Cut(strings.TrimSpace(message), "\n"); found {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(message)
}

var regexMarkers = []string{"=~", "grep -E", "grep -P", "pattern:"}

// extractRegexLiterals finds branch validators in a CI file: shell `=~`
// comparisons, `grep -E/P` patterns and `pattern:` keys. Only literals that
// compile are returned.
func extractRegexLiterals(body string) []string {
	var found []string
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		for _, marker := range regexMarkers {
			idx := strings.Index(line, marker)
			if idx < 0 {
				continue
			}
			candidate := cleanRegexLiteral(line[idx+len(marker):])
			if candidate == "" || seen[candidate] || len(candidate) > 200 {
				continue
			}
			if !strings.ContainsAny(candidate, "^$[]()|+*\\") {
				continue
			}
			if _, err := regexp.Compile(candidate); err != nil {
				continue
			}
			seen[candidate] = true
			found = append(found, candidate)
		}
	}
	return found
}

// cleanRegexLiteral trims quoting and shell suffixes around a regex token.
func cleanRegexLiteral(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if quote := raw[0]; quote == '\'' || quote == '"' || quote == '`' {
		if end := strings.IndexByte(raw[1:], quote); end >= 0 {
			raw = raw[1 : end+1]
		}
	} else if end := strings.IndexAny(raw, " \t"); end >= 0 {
		raw = raw[:end]
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ";,}")
	// A shell closing "]]" hugs the pattern in unquoted comparisons.
	for strings.HasSuffix(raw, "]") && strings.Count(raw, "]") > strings.Count(raw, "[") {
		raw = strings.TrimSuffix(raw, "]")
	}
	raw = strings.TrimSpace(raw)
	return strings.Trim(raw, "'\"`")
}
