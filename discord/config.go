package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Config struct {
	Token    string
	ClientID string
	Secret   string

	// HTTPClient is used for every request, so tests can point the provider
	// at a fake API. Nil means http.DefaultClient.
	HTTPClient *http.Client
}

type Context struct {
	Session *discordgo.Session
	Config  *Config

	version string
	bearer  bearerCache
}

// bearerCache holds the OAuth2 client-credentials token, shared by every
// resource in one run so a plan touching many of them asks Discord once.
type bearerCache struct {
	mu      sync.Mutex
	session *discordgo.Session
	expires time.Time
}

// commandPermissionsScope is the only scope that may edit application command
// permissions. A bot token cannot, whatever its permissions in the server.
const commandPermissionsScope = "applications.commands.permissions.update"

func (c *Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Config) Client(version string) (*Context, error) {
	session, err := discordgo.New(c.Token)
	if err != nil {
		return nil, err
	}
	session.UserAgent = "discord-terraform/" + version
	session.Client = c.httpClient()

	return &Context{Config: c, Session: session, version: version}, nil
}

// BearerSession returns a session authenticated as the owner of the
// application named by `client_id`, through the client-credentials grant.
//
// Discord accepts only a Bearer token for writing application command
// permissions, and the token's user must be allowed to manage the server and
// its roles. For an application owned by a single user, client credentials
// give a token for that user; for a team-owned one they do not, since the team
// is not a member of the server.
func (m *Context) BearerSession(ctx context.Context) (*discordgo.Session, error) {
	m.bearer.mu.Lock()
	defer m.bearer.mu.Unlock()

	if m.bearer.session != nil && time.Now().Before(m.bearer.expires) {
		return m.bearer.session, nil
	}
	if m.Config.ClientID == "" || m.Config.Secret == "" {
		return nil, fmt.Errorf("`client_id` and `secret` must be set on the provider (or DISCORD_CLIENT_ID and DISCORD_CLIENT_SECRET) to write application command permissions — a bot token cannot")
	}

	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {commandPermissionsScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordgo.EndpointOAuth2+"token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(m.Config.ClientID, m.Config.Secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.Config.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting a client-credentials token: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("reading the client-credentials token (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return nil, fmt.Errorf("client-credentials token refused (HTTP %d): %s %s", resp.StatusCode, body.Error, body.Description)
	}

	session, err := discordgo.New("Bearer " + body.AccessToken)
	if err != nil {
		return nil, err
	}
	session.UserAgent = "discord-terraform/" + m.version
	session.Client = m.Config.httpClient()

	m.bearer.session = session
	// A minute's margin, so a token is never used in the second it expires.
	m.bearer.expires = time.Now().Add(time.Duration(body.ExpiresIn)*time.Second - time.Minute)
	return session, nil
}
