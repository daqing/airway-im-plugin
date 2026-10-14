package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ScreeningUnavailable is recorded in place of the matched words when the
// moderation service could not answer. An operator reading the review queue
// then sees "nobody screened this" rather than a message that looks clean.
const ScreeningUnavailable = "screening-unavailable"

// ScreenResult is the moderation service's verdict for one message.
type ScreenResult struct {
	Flagged    bool     `json:"flagged"`
	Categories []string `json:"categories"`
	Words      []string `json:"words"`
}

var screenHTTPClient = &http.Client{Timeout: 1500 * time.Millisecond}

// ScreenContent asks the moderation service whether content needs review.
// The service owns the word list; this plugin keeps none of its own, so a
// failure here is reported to the caller and the send path holds the message
// back rather than delivering it unscreened.
//
// ScreenContent is a variable so a test can drive a verdict without standing
// up the service.
var ScreenContent = Screen

// Screen posts content to the moderation service.
func Screen(ctx context.Context, content string) (ScreenResult, error) {
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return ScreenResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, moderationBaseURL()+"/screen", bytes.NewReader(body))
	if err != nil {
		return ScreenResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if secret := strings.TrimSpace(os.Getenv("MODERATION_SECRET")); secret != "" {
		request.Header.Set("X-Moderation-Secret", secret)
	}
	response, err := screenHTTPClient.Do(request)
	if err != nil {
		return ScreenResult{}, fmt.Errorf("moderation: screen: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ScreenResult{}, fmt.Errorf("moderation: screen: unexpected status %d", response.StatusCode)
	}
	// The service answers errors inside the envelope, with HTTP 200, so the
	// business code is what says whether data is a verdict.
	var envelope struct {
		Code    int          `json:"code"`
		Data    ScreenResult `json:"data"`
		Message string       `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return ScreenResult{}, fmt.Errorf("moderation: screen: %w", err)
	}
	if envelope.Code != 0 {
		return ScreenResult{}, fmt.Errorf("moderation: screen: %s", envelope.Message)
	}
	return envelope.Data, nil
}

func moderationBaseURL() string {
	return strings.TrimRight(envOr("IM_MODERATION_URL", "http://127.0.0.1:1905/api/v1/moderation"), "/")
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
