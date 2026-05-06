package oauth

// ClientID and ClientSecret identify the Scout OAuth 2.0 (3LO) app
// registered at developer.atlassian.com. They are injected at release
// time via -ldflags -X. Local builds leave them empty; Login() will
// then return ErrCredentialsMissing.
//
// Example release build:
//
//	go build -ldflags "\
//	  -X scout/internal/oauth.ClientID=<client-id> \
//	  -X scout/internal/oauth.ClientSecret=<client-secret>" \
//	  ./cmd/scout
var (
	ClientID     = ""
	ClientSecret = ""
)

const (
	AuthURL                = "https://auth.atlassian.com/authorize"
	TokenURL               = "https://auth.atlassian.com/oauth/token"
	AccessibleResourcesURL = "https://api.atlassian.com/oauth/token/accessible-resources"
	APIBaseURL             = "https://api.atlassian.com/ex/jira"
	Audience               = "api.atlassian.com"
	RedirectHost           = "127.0.0.1"
	// RedirectPort is the fixed loopback port `scout jira-login` listens on
	// for the OAuth callback. Atlassian does not support wildcard or
	// `127.0.0.1`-only callback URLs in the developer console — the URL has
	// to match exactly, so this constant is the contract between the
	// registered "Scout" OAuth app and the CLI. If you change it, the
	// developer-console callback URL must change in lockstep.
	//
	// Chosen from the IANA dynamic/private range (49152–65535) and
	// deliberately uncommon to avoid colliding with other dev servers.
	RedirectPort  = 53127
	TokenFileName = "oauth_tokens.json"
)

var Scopes = []string{
	"read:jira-work",
	"read:jira-user",
	"offline_access",
}
