package airwayim

// Server-to-server client for the plugin's internal API listener
// (IM_INTERNAL_ADDR, default 127.0.0.1:1906, X-IM-Internal-Secret
// protected). Reachable only from a private network — never from end-user
// clients: whoever can mint credentials can impersonate any user, so do
// not link this type into client-facing code paths.

import (
	"context"
	"time"
)

// InternalOption customizes an InternalClient.
type InternalOption func(*InternalClient)

// WithInternalTimeout sets the per-request timeout (default 15s).
func WithInternalTimeout(timeout time.Duration) InternalOption {
	return func(c *InternalClient) { c.transport = NewHTTPTransport(timeout) }
}

// WithInternalTransport replaces the HTTP transport (tests, proxies).
func WithInternalTransport(transport HTTPTransport) InternalOption {
	return func(c *InternalClient) { c.transport = transport }
}

// InternalClient mints credentials for (uuid, name) server-to-server.
type InternalClient struct {
	baseURL        string
	internalSecret string
	transport      HTTPTransport
}

// NewInternalClient creates a client for the internal listener.
//
// internalURL is the internal listener URL, e.g. http://127.0.0.1:1906 —
// private network only; internalSecret is the deployment's
// IM_INTERNAL_SECRET.
func NewInternalClient(internalURL, internalSecret string, opts ...InternalOption) *InternalClient {
	client := &InternalClient{
		baseURL:        trimTrailingSlashes(internalURL),
		internalSecret: internalSecret,
		transport:      NewHTTPTransport(15 * time.Second),
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

// MintOptions carries the MintCredential inputs. Nickname and AvatarURL
// are write-only-when-present: omitting them never clobbers a richer
// profile stored earlier in the IM users table.
type MintOptions struct {
	UUID string
	Name string
	// Nickname is written only when non-empty.
	Nickname string
	// AvatarURL is written only when non-empty.
	AvatarURL string
	// TTLSeconds: nil applies the backend default (86400, capped at
	// 2592000); 0 omits the expiry claim (service credentials only).
	TTLSeconds *int
}

// MintCredential mints a client credential for (uuid, name)
// server-to-server. The user is registered with IM on the first mint
// under that uuid (the same uuid clients pass to CreateDirect /
// SendDirectMessage); the credential carries the user's current
// token_version, so revoking the user through the admin API invalidates
// every credential minted before it.
func (c *InternalClient) MintCredential(ctx context.Context, opts MintOptions) (MintedCredential, error) {
	body := struct {
		UUID       string `json:"uuid"`
		Name       string `json:"name"`
		Nickname   string `json:"nickname,omitempty"`
		AvatarURL  string `json:"avatar_url,omitempty"`
		TTLSeconds *int   `json:"ttl_seconds,omitempty"`
	}{
		UUID:       opts.UUID,
		Name:       opts.Name,
		Nickname:   opts.Nickname,
		AvatarURL:  opts.AvatarURL,
		TTLSeconds: opts.TTLSeconds,
	}
	payload, err := marshalBody(body)
	if err != nil {
		return MintedCredential{}, imErr(err)
	}
	response, err := c.transport.Send(ctx, &HTTPRequest{
		URL:    joinURL(c.baseURL, "/internal/v1/credentials"),
		Method: "POST",
		Headers: map[string]string{
			"Content-Type":         "application/json",
			"X-IM-Internal-Secret": c.internalSecret,
		},
		Body: payload,
	})
	if err != nil {
		// Transport failure (including timeout): no HTTP status, the
		// caller may retry.
		return MintedCredential{}, imErr(err)
	}
	var data mintData
	if err := unwrapEnvelope(response, &data); err != nil {
		return MintedCredential{}, err
	}
	minted := MintedCredential{Credential: data.Credential}
	if data.ExpiresAt != nil {
		minted.ExpiresAt = *data.ExpiresAt
	}
	return minted, nil
}

// Notify pushes a host-domain notification to the given users over the
// WebSocket gateway chat uses, instead of the host building its own push
// channel. event is a free-form name (conventionally prefixed "host.",
// e.g. "host.friend_request"), data any JSON value. Limits enforced
// server-side: at most 100 recipients, event names 1–64 characters, data
// at most 4 KiB encoded. Delivery is advisory: online recipients receive
// the frame in real time, offline recipients are not replayed — pair each
// event with a pull endpoint clients load on demand. Returns the
// generated event id.
func (c *InternalClient) Notify(ctx context.Context, userUUIDs []string, event string, data any) (string, error) {
	body := struct {
		UserUUIDs []string `json:"user_uuids"`
		Event     string   `json:"event"`
		Data      any      `json:"data"`
	}{
		UserUUIDs: userUUIDs,
		Event:     event,
		Data:      data,
	}
	payload, err := marshalBody(body)
	if err != nil {
		return "", imErr(err)
	}
	response, err := c.transport.Send(ctx, &HTTPRequest{
		URL:    joinURL(c.baseURL, "/internal/v1/notify"),
		Method: "POST",
		Headers: map[string]string{
			"Content-Type":         "application/json",
			"X-IM-Internal-Secret": c.internalSecret,
		},
		Body: payload,
	})
	if err != nil {
		// Transport failure (including timeout): no HTTP status, the
		// caller may retry.
		return "", imErr(err)
	}
	var result struct {
		EventID string `json:"event_id"`
	}
	if err := unwrapEnvelope(response, &result); err != nil {
		return "", err
	}
	return result.EventID, nil
}
