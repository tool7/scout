package githubauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	deviceFlowHTTPTimeout = 30 * time.Second
	userAgent             = "scout-cli"

	// slowDownIncrement is the extra polling interval RFC 8628 mandates
	// when the server returns "slow_down".
	slowDownIncrement = 5 * time.Second
)

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Login runs the GitHub Device Flow end-to-end and returns the access
// token on success. The caller is responsible for persisting it.
//
//  1. POST /login/device/code   → user_code + device_code + verification_uri + interval
//  2. Print the user code, attempt to open the browser to verification_uri
//  3. Poll POST /login/oauth/access_token with the device_code until
//     the user approves, declines, or the code expires
func Login(ctx context.Context) (string, error) {
	if ClientID == "" {
		return "", ErrCredentialsMissing
	}

	client := &http.Client{Timeout: deviceFlowHTTPTimeout}

	dc, err := requestDeviceCode(ctx, client)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr,
		"To authorize scout, visit:\n  %s\nand enter the code:\n\n  %s\n\n",
		dc.VerificationURI, dc.UserCode,
	)
	if err := openBrowser(dc.VerificationURI); err != nil {
		fmt.Fprintf(os.Stderr, "(could not auto-open browser: %v — open the URL above manually)\n", err)
	}
	fmt.Fprintln(os.Stderr, "Waiting for authorization...")

	return pollAccessToken(ctx, client, dc)
}

func requestDeviceCode(ctx context.Context, client *http.Client) (*deviceCodeResponse, error) {
	form := url.Values{
		"client_id": {ClientID},
		"scope":     {ScopesSpace},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, DeviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("GitHub device flow failed (request device code): build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub device flow failed (request device code): %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GitHub device flow failed (request device code): read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub device flow failed (request device code): HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out deviceCodeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("GitHub device flow failed (request device code): unmarshal: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" || out.VerificationURI == "" {
		// Surface the device_flow_disabled case loudly — it means the
		// OAuth App registration is missing the "Enable Device Flow"
		// toggle, which is the most common deploy-time misconfiguration.
		return nil, fmt.Errorf("GitHub device flow failed: response missing device_code/user_code/verification_uri (is 'Enable Device Flow' checked on the OAuth App?)")
	}
	if out.Interval == 0 {
		out.Interval = 5
	}
	return &out, nil
}

func pollAccessToken(ctx context.Context, client *http.Client, dc *deviceCodeResponse) (string, error) {
	interval := time.Duration(dc.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("GitHub device flow failed: user code %s expired before authorization completed; re-run `scout github-login`", dc.UserCode)
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}

		tok, retryable, slowDown, err := pollOnce(ctx, client, dc.DeviceCode)
		if err != nil {
			return "", err
		}
		if tok != "" {
			return tok, nil
		}
		if slowDown {
			interval += slowDownIncrement
		}
		if !retryable {
			return "", fmt.Errorf("GitHub device flow failed: unexpected non-retryable empty response")
		}
	}
}

// pollOnce performs a single poll against the access-token endpoint.
// Returns (token, retryable, slowDown, err). A non-empty token means
// success; otherwise retryable=true means keep polling (with slowDown
// indicating whether the interval should grow), retryable=false plus
// err means a terminal error.
func pollOnce(ctx context.Context, client *http.Client, deviceCode string) (string, bool, bool, error) {
	form := url.Values{
		"client_id":   {ClientID},
		"device_code": {deviceCode},
		"grant_type":  {DeviceFlowGrantType},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, AccessTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, false, fmt.Errorf("GitHub device flow failed (poll): build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", false, false, fmt.Errorf("GitHub device flow failed (poll): %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, false, fmt.Errorf("GitHub device flow failed (poll): read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, false, fmt.Errorf("GitHub device flow failed (poll): HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", false, false, fmt.Errorf("GitHub device flow failed (poll): unmarshal: %w", err)
	}

	if tr.AccessToken != "" {
		return tr.AccessToken, false, false, nil
	}

	switch tr.Error {
	case "authorization_pending":
		return "", true, false, nil
	case "slow_down":
		return "", true, true, nil
	case "expired_token":
		return "", false, false, fmt.Errorf("GitHub device flow failed: device code expired; re-run `scout github-login`")
	case "access_denied":
		return "", false, false, fmt.Errorf("GitHub device flow failed: user declined authorization")
	case "device_flow_disabled":
		return "", false, false, fmt.Errorf("GitHub device flow failed: device flow is disabled on this OAuth App (enable it in the GitHub OAuth App settings)")
	case "incorrect_client_credentials":
		return "", false, false, fmt.Errorf("GitHub device flow failed: client_id %q is not recognised by GitHub", ClientID)
	default:
		detail := tr.ErrorDescription
		if detail == "" {
			detail = tr.Error
		}
		return "", false, false, fmt.Errorf("GitHub device flow failed: %s", detail)
	}
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
