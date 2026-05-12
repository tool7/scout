package githubauth

import "errors"

// ClientID is the OAuth App client identifier injected at link time via
// `-X scout/internal/githubauth.ClientID=...`. Builds without the
// injection have an empty ClientID and Login() will refuse to run.
var ClientID = ""

const (
	DeviceCodeURL  = "https://github.com/login/device/code"
	AccessTokenURL = "https://github.com/login/oauth/access_token"
	UserAPIURL     = "https://api.github.com/user"

	// DeviceFlowGrantType is the RFC 8628 grant type identifier required
	// on every poll of AccessTokenURL.
	DeviceFlowGrantType = "urn:ietf:params:oauth:grant-type:device_code"
)

// Scopes is the OAuth scope set requested at device-code time. `repo`
// is the minimum that lets scout read PRs and reviews across both
// public and private repositories — classic OAuth Apps have no
// read-only repo scope to fall back on.
var Scopes = []string{"repo"}

// ScopesSpace is the space-joined wire form of Scopes, used directly in
// the form-encoded request body.
const ScopesSpace = "repo"

var ErrCredentialsMissing = errors.New("github oauth client id is not embedded in this build (rebuild with -ldflags -X scout/internal/githubauth.ClientID=...)")
