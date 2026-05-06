package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var ErrCredentialsMissing = errors.New("oauth client credentials are not embedded in this build (rebuild with -ldflags -X scout/internal/oauth.ClientID=... -X scout/internal/oauth.ClientSecret=...)")

// Login runs the full Atlassian 3LO flow against the user's browser.
// jiraHost is the configured https://your-org.atlassian.net so that
// when accessible-resources returns multiple Atlassian sites we can
// pick the right cloudId. On success the returned Tokens are also
// persisted to <dataDir>/oauth_tokens.json.
func Login(ctx context.Context, dataDir, jiraHost string) (Tokens, error) {
	if ClientID == "" || ClientSecret == "" {
		return Tokens{}, ErrCredentialsMissing
	}

	addr := fmt.Sprintf("%s:%d", RedirectHost, RedirectPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return Tokens{}, fmt.Errorf("bind callback listener on %s: %w (is another scout login already running, or another process holding the port?)", addr, err)
	}
	redirectURL := fmt.Sprintf("http://%s/callback", addr)

	state, err := randomString(32)
	if err != nil {
		return Tokens{}, fmt.Errorf("generate state: %w", err)
	}
	verifier := oauth2.GenerateVerifier()

	cfg := oauthConfig(redirectURL)
	authURL := cfg.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("audience", Audience),
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
	)

	type result struct {
		token *oauth2.Token
		err   error
	}
	resultCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errStr := q.Get("error"); errStr != "" {
			msg := errStr
			if d := q.Get("error_description"); d != "" {
				msg += ": " + d
			}
			renderCallback(w, false, msg)
			resultCh <- result{err: fmt.Errorf("authorization denied: %s", msg)}
			return
		}
		if got := q.Get("state"); got != state {
			renderCallback(w, false, "state mismatch")
			resultCh <- result{err: errors.New("state mismatch — possible CSRF, aborting")}
			return
		}
		code := q.Get("code")
		if code == "" {
			renderCallback(w, false, "missing authorization code")
			resultCh <- result{err: errors.New("authorization callback missing code")}
			return
		}
		tok, err := cfg.Exchange(r.Context(), code, oauth2.VerifierOption(verifier))
		if err != nil {
			renderCallback(w, false, err.Error())
			resultCh <- result{err: fmt.Errorf("exchange code for token: %w", err)}
			return
		}
		renderCallback(w, true, "")
		resultCh <- result{token: tok}
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "Opening browser to authorize Scout against Jira.\nIf nothing opens, visit:\n  %s\n", authURL)
	_ = openBrowser(authURL)

	loginCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var tok *oauth2.Token
	select {
	case <-loginCtx.Done():
		return Tokens{}, fmt.Errorf("login timed out or was cancelled: %w", loginCtx.Err())
	case res := <-resultCh:
		if res.err != nil {
			return Tokens{}, res.err
		}
		tok = res.token
	}

	cloudID, scopeGrant, err := pickCloudID(ctx, tok.AccessToken, jiraHost)
	if err != nil {
		return Tokens{}, err
	}

	tokens := Tokens{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		ExpiresAt:    tok.Expiry,
		CloudID:      cloudID,
		Scope:        scopeGrant,
	}
	if err := Save(dataDir, tokens); err != nil {
		return Tokens{}, err
	}
	return tokens, nil
}

type accessibleResource struct {
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func pickCloudID(ctx context.Context, accessToken, jiraHost string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AccessibleResourcesURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build accessible-resources request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("call accessible-resources: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read accessible-resources: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("accessible-resources returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var resources []accessibleResource
	if err := json.Unmarshal(body, &resources); err != nil {
		return "", "", fmt.Errorf("parse accessible-resources: %w", err)
	}
	if len(resources) == 0 {
		return "", "", errors.New("Atlassian returned no accessible Jira sites for this account")
	}

	wanted := normalizeHost(jiraHost)
	for _, r := range resources {
		if normalizeHost(r.URL) == wanted {
			return r.ID, strings.Join(r.Scopes, " "), nil
		}
	}

	available := make([]string, 0, len(resources))
	for _, r := range resources {
		available = append(available, r.URL)
	}
	return "", "", fmt.Errorf("no accessible Jira site matches configured host %q. Available: %s", jiraHost, strings.Join(available, ", "))
}

func normalizeHost(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return strings.TrimRight(strings.ToLower(s), "/")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

func renderCallback(w http.ResponseWriter, ok bool, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Scout</title>
<body style="font-family:system-ui;padding:2rem">
<h2>Logged in to Jira</h2>
<p>You can close this tab and return to your terminal.</p>
</body>`)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Scout</title>
<body style="font-family:system-ui;padding:2rem">
<h2>Login failed</h2>
<pre>%s</pre>
</body>`, errMsg)
}
