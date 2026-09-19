package admin_api

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/deps/im/app/repo"
	"github.com/gin-gonic/gin"
)

var metricsHTTPClient = &http.Client{Timeout: 3 * time.Second}

type systemStatus struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Users       userStats      `json:"users"`
	Online      onlineStatus   `json:"online"`
	Outbox      outboxStats    `json:"outbox_publisher"`
	Gateway     serviceMetrics `json:"gateway"`
	Delivery    serviceMetrics `json:"delivery"`
}

type userStats struct {
	Total int64 `json:"total"`
}

type onlineStatus struct {
	Available bool    `json:"available"`
	Error     *string `json:"error"`
	UserIDs   []int64 `json:"user_ids"`
}

type outboxStats struct {
	Pending          int64   `json:"pending"`
	Published        int64   `json:"published"`
	FailedAttempts   int64   `json:"failed_attempts"`
	OldestPendingSec float64 `json:"oldest_pending_seconds"`
}

type serviceMetrics struct {
	Available bool               `json:"available"`
	Error     *string            `json:"error"`
	Values    map[string]float64 `json:"values"`
}

func Status(c *gin.Context) {
	status, err := loadDatabaseStatus()
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not load system status")
		return
	}
	ctx := c.Request.Context()
	status.Gateway = fetchMetrics(ctx, envOr("ADMIN_GATEWAY_METRICS_URL", "http://127.0.0.1:1910/metrics"))
	status.Delivery = fetchMetrics(ctx, envOr("ADMIN_DELIVERY_METRICS_URL", "http://127.0.0.1:1920/metrics"))
	status.Online = fetchOnline(ctx)
	adminOK(c, status)
}

// fetchOnline asks the gateway for its currently connected user IDs. With
// multiple gateway instances only the configured one is queried; the caller
// merges per-instance lists.
func fetchOnline(ctx context.Context) onlineStatus {
	result := onlineStatus{UserIDs: []int64{}}
	endpoint := strings.TrimRight(gatewayBaseURL(), "/") + "/internal/v1/online"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unavailableOnline(err)
	}
	request.Header.Set("X-IM-Internal-Secret", envOr("IM_INTERNAL_SECRET", ""))
	response, err := metricsHTTPClient.Do(request)
	if err != nil {
		return unavailableOnline(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return unavailableOnline(fmt.Errorf("online endpoint returned %s", response.Status))
	}
	var body struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return unavailableOnline(err)
	}
	result.Available = true
	if body.UserIDs != nil {
		result.UserIDs = body.UserIDs
	}
	return result
}

func unavailableOnline(err error) onlineStatus {
	message := err.Error()
	return onlineStatus{Available: false, Error: &message, UserIDs: []int64{}}
}

func loadDatabaseStatus() (systemStatus, error) {
	db := repo.CurrentDB()
	status := systemStatus{GeneratedAt: time.Now().UTC()}
	if err := db.Get(&status.Users.Total, "SELECT COUNT(*) FROM users"); err != nil {
		return status, err
	}
	if err := db.Get(&status.Outbox.Pending, "SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL"); err != nil {
		return status, err
	}
	if err := db.Get(&status.Outbox.Published, "SELECT COUNT(*) FROM outbox_events WHERE published_at IS NOT NULL"); err != nil {
		return status, err
	}
	if err := db.Get(&status.Outbox.FailedAttempts, "SELECT COALESCE(SUM(attempts), 0) FROM outbox_events WHERE published_at IS NULL"); err != nil {
		return status, err
	}
	var oldest sql.NullTime
	if err := db.Get(&oldest, "SELECT MIN(created_at) FROM outbox_events WHERE published_at IS NULL"); err != nil {
		return status, err
	}
	if oldest.Valid {
		status.Outbox.OldestPendingSec = time.Since(oldest.Time.UTC()).Seconds()
		if status.Outbox.OldestPendingSec < 0 {
			status.Outbox.OldestPendingSec = 0
		}
	}
	return status, nil
}

func fetchMetrics(ctx context.Context, endpoint string) serviceMetrics {
	result := serviceMetrics{Values: map[string]float64{}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unavailableMetrics(err)
	}
	response, err := metricsHTTPClient.Do(req)
	if err != nil {
		return unavailableMetrics(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return unavailableMetrics(fmt.Errorf("metrics endpoint returned %s", response.Status))
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		value, err := strconv.ParseFloat(parts[1], 64)
		if err == nil {
			result.Values[parts[0]] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return unavailableMetrics(err)
	}
	result.Available = true
	return result
}

func unavailableMetrics(err error) serviceMetrics {
	message := err.Error()
	return serviceMetrics{Available: false, Error: &message, Values: map[string]float64{}}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
