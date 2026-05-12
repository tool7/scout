package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scout/internal/config"
	"scout/internal/logger"
)

const gitHubAPIBaseURL = "https://api.github.com"

type gitHubUser struct {
	Login string `json:"login"`
}

type gitHubRef struct {
	Ref string `json:"ref"`
}

type gitHubPullRequest struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     string     `json:"state"`
	User      gitHubUser `json:"user"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	MergedAt  string     `json:"merged_at"`
	Head      gitHubRef  `json:"head"`
	Base      gitHubRef  `json:"base"`
	HTMLURL   string     `json:"html_url"`
}

type gitHubReview struct {
	User        gitHubUser `json:"user"`
	Body        string     `json:"body"`
	SubmittedAt string     `json:"submitted_at"`
	State       string     `json:"state"`
}

type gitHubClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

func newGitHubClient(token string) *gitHubClient {
	return &gitHubClient{
		httpClient: &http.Client{Timeout: prHTTPTimeout},
		baseURL:    gitHubAPIBaseURL,
		token:      token,
	}
}

func (c *gitHubClient) do(ctx context.Context, path string, label string) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("GitHub API call failed (%s): build request: %w", label, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "scout-cli")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("GitHub API call failed (%s): %w", label, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp, fmt.Errorf("GitHub API call failed (%s): read response: %w", label, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		return nil, resp, fmt.Errorf("GitHub API call failed (%s): HTTP %d %s", label, resp.StatusCode, detail)
	}
	return body, resp, nil
}

// respectRateLimit pauses the goroutine when GitHub signals that we
// have exhausted the per-token budget. We only act on the secondary
// signal (X-RateLimit-Remaining == 0) so single in-flight requests
// don't pre-empt themselves on every call.
func (c *gitHubClient) respectRateLimit(ctx context.Context, resp *http.Response) error {
	if resp == nil {
		return nil
	}
	remainingStr := resp.Header.Get("X-RateLimit-Remaining")
	resetStr := resp.Header.Get("X-RateLimit-Reset")
	if remainingStr == "" || resetStr == "" {
		return nil
	}
	remaining, err := strconv.Atoi(remainingStr)
	if err != nil || remaining > 0 {
		return nil
	}
	resetUnix, err := strconv.ParseInt(resetStr, 10, 64)
	if err != nil {
		return nil
	}
	wait := time.Until(time.Unix(resetUnix, 0))
	if wait <= 0 {
		return nil
	}
	logger.Warnf("GitHub rate limit hit; sleeping %s until reset", wait.Round(time.Second))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

func SyncGitHubProject(ctx context.Context, db *sql.DB, project config.Project, dataDir string, opts PRSyncOptions) (PRSyncResult, error) {
	token, err := LoadGitHubToken(dataDir)
	if err != nil {
		return PRSyncResult{}, err
	}
	client := newGitHubClient(token)

	owner, repo, ok := splitGitHubRepo(project.GitHubRepo)
	if !ok {
		return PRSyncResult{}, fmt.Errorf("project %q has invalid githubRepo %q (expected 'owner/repo')", project.Name, project.GitHubRepo)
	}

	var since string
	if !opts.Full {
		s, _, err := resolveIncrementalPRSince(db, project.Name)
		if err != nil {
			return PRSyncResult{}, err
		}
		since = s
	}
	mode := "full"
	if since != "" {
		mode = "incremental since " + since
	}
	logger.Infof("[%s] fetching GitHub PRs for %s/%s (%s)", project.Name, owner, repo, mode)

	pulls, err := fetchGitHubPulls(ctx, client, owner, repo, since)
	if err != nil {
		return PRSyncResult{}, err
	}
	logger.Infof("[%s] fetched %d PR(s); now collecting reviews", project.Name, len(pulls))

	prs := make([]parsedPR, 0, len(pulls))
	for i, p := range pulls {
		reviews, err := fetchGitHubReviews(ctx, client, owner, repo, p.Number)
		if err != nil {
			return PRSyncResult{}, err
		}
		prs = append(prs, toGitHubParsedPR(p, reviews, project.Name))
		if (i+1)%50 == 0 {
			logger.Infof("[%s] fetched reviews for %d/%d PRs", project.Name, i+1, len(pulls))
		}
	}

	upserted, err := upsertPRs(db, prs)
	if err != nil {
		return PRSyncResult{}, err
	}
	if err := rebuildPRsFTS(db); err != nil {
		return PRSyncResult{}, err
	}
	total, err := countProjectPRs(db, project.Name)
	if err != nil {
		return PRSyncResult{}, err
	}
	if err := updatePRSyncState(db, project.Name, total); err != nil {
		return PRSyncResult{}, err
	}

	logger.Infof("[%s] upserted %d PR(s); %d total in database", project.Name, upserted, total)
	return PRSyncResult{Project: project.Name, PRCount: upserted}, nil
}

func splitGitHubRepo(slug string) (owner, repo string, ok bool) {
	parts := strings.SplitN(slug, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func fetchGitHubPulls(ctx context.Context, client *gitHubClient, owner, repo, since string) ([]gitHubPullRequest, error) {
	var all []gitHubPullRequest
	for page := 1; ; page++ {
		path := fmt.Sprintf(
			"/repos/%s/%s/pulls?state=all&per_page=100&sort=updated&direction=desc&page=%d",
			url.PathEscape(owner), url.PathEscape(repo), page,
		)
		body, resp, err := client.do(ctx, path, "list pulls")
		if err != nil {
			return nil, err
		}
		if err := client.respectRateLimit(ctx, resp); err != nil {
			return nil, err
		}

		var pulls []gitHubPullRequest
		if err := json.Unmarshal(body, &pulls); err != nil {
			return nil, fmt.Errorf("GitHub API call failed (list pulls): unmarshal: %w", err)
		}
		if len(pulls) == 0 {
			break
		}

		stop := false
		if since != "" {
			oldest := pulls[len(pulls)-1].UpdatedAt
			if oldest != "" && oldest < since {
				stop = true
			}
			filtered := pulls[:0]
			for _, p := range pulls {
				if p.UpdatedAt >= since {
					filtered = append(filtered, p)
				}
			}
			pulls = filtered
		}
		all = append(all, pulls...)

		if stop || len(pulls) < 100 {
			break
		}
	}
	return all, nil
}

func fetchGitHubReviews(ctx context.Context, client *gitHubClient, owner, repo string, number int) ([]gitHubReview, error) {
	var all []gitHubReview
	for page := 1; ; page++ {
		path := fmt.Sprintf(
			"/repos/%s/%s/pulls/%d/reviews?per_page=100&page=%d",
			url.PathEscape(owner), url.PathEscape(repo), number, page,
		)
		body, resp, err := client.do(ctx, path, fmt.Sprintf("list reviews #%d", number))
		if err != nil {
			return nil, err
		}
		if err := client.respectRateLimit(ctx, resp); err != nil {
			return nil, err
		}
		var reviews []gitHubReview
		if err := json.Unmarshal(body, &reviews); err != nil {
			return nil, fmt.Errorf("GitHub API call failed (list reviews): unmarshal: %w", err)
		}
		all = append(all, reviews...)
		if len(reviews) < 100 {
			break
		}
	}
	return all, nil
}

func toGitHubParsedPR(p gitHubPullRequest, reviews []gitHubReview, projectName string) parsedPR {
	state := "open"
	switch {
	case p.MergedAt != "":
		state = "merged"
	case p.State == "closed":
		state = "closed"
	}

	comments := make([]prReviewComment, 0, len(reviews))
	for _, r := range reviews {
		body := strings.TrimSpace(r.Body)
		if body == "" {
			continue
		}
		comments = append(comments, prReviewComment{
			Author:      r.User.Login,
			Body:        r.Body,
			SubmittedAt: r.SubmittedAt,
		})
	}

	return parsedPR{
		id:             fmt.Sprintf("gh#%d", p.Number),
		project:        projectName,
		provider:       "github",
		number:         p.Number,
		title:          p.Title,
		body:           p.Body,
		state:          state,
		author:         p.User.Login,
		createdAt:      p.CreatedAt,
		updatedAt:      p.UpdatedAt,
		mergedAt:       p.MergedAt,
		sourceBranch:   p.Head.Ref,
		targetBranch:   p.Base.Ref,
		url:            p.HTMLURL,
		reviewComments: comments,
	}
}

// ValidateGitHubToken makes a single authenticated request against
// /user. Used by `scout github-login` to surface bad PATs immediately
// rather than waiting for the next sync.
func ValidateGitHubToken(ctx context.Context, token string) (string, error) {
	client := newGitHubClient(token)
	body, _, err := client.do(ctx, "/user", "validate token")
	if err != nil {
		return "", err
	}
	var u gitHubUser
	if err := json.Unmarshal(body, &u); err != nil {
		return "", fmt.Errorf("GitHub API call failed (validate token): unmarshal: %w", err)
	}
	return u.Login, nil
}
