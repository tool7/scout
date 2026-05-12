package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"scout/internal/config"
	"scout/internal/logger"
)

const bitbucketAPIBaseURL = "https://api.bitbucket.org/2.0"

type bitbucketUser struct {
	DisplayName string `json:"display_name"`
}

type bitbucketBranch struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
}

type bitbucketLinks struct {
	HTML struct {
		Href string `json:"href"`
	} `json:"html"`
}

type bitbucketMergeCommit struct {
	Date string `json:"date"`
}

type bitbucketPullRequest struct {
	ID           int                  `json:"id"`
	Title        string               `json:"title"`
	Description  string               `json:"description"`
	State        string               `json:"state"`
	Author       bitbucketUser        `json:"author"`
	CreatedOn    string               `json:"created_on"`
	UpdatedOn    string               `json:"updated_on"`
	MergeCommit  bitbucketMergeCommit `json:"merge_commit"`
	Source       bitbucketBranch      `json:"source"`
	Destination  bitbucketBranch      `json:"destination"`
	Links        bitbucketLinks       `json:"links"`
	CommentCount int                  `json:"comment_count"`
}

type bitbucketComment struct {
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	User      bitbucketUser `json:"user"`
	CreatedOn string        `json:"created_on"`
	Deleted   bool          `json:"deleted"`
}

type bitbucketPage[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}

type bitbucketClient struct {
	httpClient  *http.Client
	baseURL     string
	email    string
	apiToken string
}

func newBitbucketClient(email, apiToken string) *bitbucketClient {
	return &bitbucketClient{
		httpClient: &http.Client{Timeout: prHTTPTimeout},
		baseURL:    bitbucketAPIBaseURL,
		email:      email,
		apiToken:   apiToken,
	}
}

func (c *bitbucketClient) doURL(ctx context.Context, fullURL, label string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("Bitbucket API call failed (%s): build request: %w", label, err)
	}
	req.SetBasicAuth(c.email, c.apiToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "scout-cli")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Bitbucket API call failed (%s): %w", label, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Bitbucket API call failed (%s): read response: %w", label, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		return nil, fmt.Errorf("Bitbucket API call failed (%s): HTTP %d %s", label, resp.StatusCode, detail)
	}
	return body, nil
}

func (c *bitbucketClient) doPath(ctx context.Context, path, label string) ([]byte, error) {
	return c.doURL(ctx, c.baseURL+path, label)
}

func SyncBitbucketProject(ctx context.Context, db *sql.DB, project config.Project, dataDir string, opts PRSyncOptions) (PRSyncResult, error) {
	email, apiToken, err := LoadBitbucketCreds(dataDir)
	if err != nil {
		return PRSyncResult{}, err
	}
	client := newBitbucketClient(email, apiToken)

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
	logger.Infof(
		"[%s] fetching Bitbucket PRs for %s/%s (%s)",
		project.Name, project.BitbucketWorkspace, project.BitbucketRepo, mode,
	)

	pulls, err := fetchBitbucketPulls(ctx, client, project.BitbucketWorkspace, project.BitbucketRepo, since)
	if err != nil {
		return PRSyncResult{}, err
	}
	logger.Infof("[%s] fetched %d PR(s); now collecting comments", project.Name, len(pulls))

	prs := make([]parsedPR, 0, len(pulls))
	for i, p := range pulls {
		comments, err := fetchBitbucketComments(ctx, client, project.BitbucketWorkspace, project.BitbucketRepo, p.ID)
		if err != nil {
			return PRSyncResult{}, err
		}
		prs = append(prs, toBitbucketParsedPR(p, comments, project.Name))
		if (i+1)%50 == 0 {
			logger.Infof("[%s] fetched comments for %d/%d PRs", project.Name, i+1, len(pulls))
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

func fetchBitbucketPulls(ctx context.Context, client *bitbucketClient, workspace, repoSlug, since string) ([]bitbucketPullRequest, error) {
	initialPath := fmt.Sprintf(
		"/repositories/%s/%s/pullrequests?state=OPEN&state=MERGED&state=DECLINED&state=SUPERSEDED&pagelen=50&sort=-updated_on",
		url.PathEscape(workspace), url.PathEscape(repoSlug),
	)
	nextURL := client.baseURL + initialPath

	var all []bitbucketPullRequest
	for nextURL != "" {
		body, err := client.doURL(ctx, nextURL, "list pulls")
		if err != nil {
			return nil, err
		}
		var page bitbucketPage[bitbucketPullRequest]
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("Bitbucket API call failed (list pulls): unmarshal: %w", err)
		}
		if len(page.Values) == 0 {
			break
		}

		stop := false
		pulls := page.Values
		if since != "" {
			oldest := pulls[len(pulls)-1].UpdatedOn
			if oldest != "" && oldest < since {
				stop = true
			}
			filtered := pulls[:0]
			for _, p := range pulls {
				if p.UpdatedOn >= since {
					filtered = append(filtered, p)
				}
			}
			pulls = filtered
		}
		all = append(all, pulls...)

		if stop {
			break
		}
		nextURL = page.Next
	}
	return all, nil
}

func fetchBitbucketComments(ctx context.Context, client *bitbucketClient, workspace, repoSlug string, id int) ([]bitbucketComment, error) {
	initialPath := fmt.Sprintf(
		"/repositories/%s/%s/pullrequests/%d/comments?pagelen=50",
		url.PathEscape(workspace), url.PathEscape(repoSlug), id,
	)
	nextURL := client.baseURL + initialPath

	var all []bitbucketComment
	for nextURL != "" {
		body, err := client.doURL(ctx, nextURL, fmt.Sprintf("list comments #%d", id))
		if err != nil {
			return nil, err
		}
		var page bitbucketPage[bitbucketComment]
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("Bitbucket API call failed (list comments): unmarshal: %w", err)
		}
		all = append(all, page.Values...)
		nextURL = page.Next
	}
	return all, nil
}

func toBitbucketParsedPR(p bitbucketPullRequest, comments []bitbucketComment, projectName string) parsedPR {
	state := normalizeBitbucketState(p.State)

	out := make([]prReviewComment, 0, len(comments))
	for _, c := range comments {
		if c.Deleted {
			continue
		}
		body := strings.TrimSpace(c.Content.Raw)
		if body == "" {
			continue
		}
		out = append(out, prReviewComment{
			Author:      c.User.DisplayName,
			Body:        c.Content.Raw,
			SubmittedAt: c.CreatedOn,
		})
	}

	return parsedPR{
		id:             fmt.Sprintf("bb#%d", p.ID),
		project:        projectName,
		provider:       "bitbucket",
		number:         p.ID,
		title:          p.Title,
		body:           p.Description,
		state:          state,
		author:         p.Author.DisplayName,
		createdAt:      p.CreatedOn,
		updatedAt:      p.UpdatedOn,
		mergedAt:       p.MergeCommit.Date,
		sourceBranch:   p.Source.Branch.Name,
		targetBranch:   p.Destination.Branch.Name,
		url:            p.Links.HTML.Href,
		reviewComments: out,
	}
}

func normalizeBitbucketState(state string) string {
	switch state {
	case "OPEN":
		return "open"
	case "MERGED":
		return "merged"
	default:
		return "closed"
	}
}

// ValidateBitbucketCreds confirms the email/api-token combination by
// hitting /user. Used by `scout bitbucket-login` to fail fast.
func ValidateBitbucketCreds(ctx context.Context, email, apiToken string) (string, error) {
	client := newBitbucketClient(email, apiToken)
	body, err := client.doPath(ctx, "/user", "validate credentials")
	if err != nil {
		return "", err
	}
	var u bitbucketUser
	if err := json.Unmarshal(body, &u); err != nil {
		return "", fmt.Errorf("Bitbucket API call failed (validate credentials): unmarshal: %w", err)
	}
	return u.DisplayName, nil
}
