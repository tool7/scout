package oauth

import (
	"context"
	"net/http"

	"golang.org/x/oauth2"
)

// HTTPClient returns an *http.Client that automatically attaches a
// Bearer token to every request and refreshes it when expired,
// persisting the new token back to disk after every refresh.
//
// It also returns the cloudId the user authorized, which the caller
// uses to build Jira REST URLs of the form
// https://api.atlassian.com/ex/jira/{cloudId}/rest/api/3/...
func HTTPClient(ctx context.Context, dataDir string) (*http.Client, string, error) {
	if ClientID == "" || ClientSecret == "" {
		return nil, "", ErrCredentialsMissing
	}
	tokens, err := Load(dataDir)
	if err != nil {
		return nil, "", err
	}
	if tokens.CloudID == "" {
		return nil, "", ErrNotLoggedIn
	}

	cfg := oauthConfig("")
	src := newPersistingSource(ctx, cfg, tokens, dataDir)
	return oauth2.NewClient(ctx, src), tokens.CloudID, nil
}

func oauthConfig(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     ClientID,
		ClientSecret: ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  AuthURL,
			TokenURL: TokenURL,
		},
		Scopes:      Scopes,
		RedirectURL: redirectURL,
	}
}
