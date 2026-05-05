package oauth

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
)

// persistingSource wraps an oauth2.TokenSource so that whenever the
// access token is refreshed, the new token (including a possibly
// rotated refresh token — Atlassian rotates them) is written back to
// disk. Without this, a refresh would invalidate the on-disk
// refresh_token and the next run would have to re-login.
type persistingSource struct {
	inner   oauth2.TokenSource
	dataDir string
	cloudID string
	scope   string
	last    string
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.inner.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken == p.last {
		return tok, nil
	}
	p.last = tok.AccessToken
	stored := Tokens{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		ExpiresAt:    tok.Expiry,
		CloudID:      p.cloudID,
		Scope:        p.scope,
	}
	if err := Save(p.dataDir, stored); err != nil {
		return nil, fmt.Errorf("persist refreshed token: %w", err)
	}
	return tok, nil
}

func newPersistingSource(ctx context.Context, cfg *oauth2.Config, t Tokens, dataDir string) oauth2.TokenSource {
	tok := &oauth2.Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		Expiry:       t.ExpiresAt,
	}
	return &persistingSource{
		inner:   cfg.TokenSource(ctx, tok),
		dataDir: dataDir,
		cloudID: t.CloudID,
		scope:   t.Scope,
		last:    t.AccessToken,
	}
}
