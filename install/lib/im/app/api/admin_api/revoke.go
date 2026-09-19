package admin_api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
	"github.com/gin-gonic/gin"
)

var kickHTTPClient = &http.Client{Timeout: 3 * time.Second}

// RevokeUserCredentials bumps the user's token_version, invalidating every
// versioned credential minted for them, and best-effort kicks their live
// gateway connections. Unversioned (host-minted) credentials stay valid
// until they expire; hosts can also simply stop minting new ones.
func RevokeUserCredentials(c *gin.Context) {
	uuid := strings.TrimSpace(c.Param("user_uuid"))
	user, found, err := auth.RevokeUser(uuid)
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not revoke credentials")
		return
	}
	if !found {
		adminError(c, http.StatusNotFound, 20001, "Unknown user")
		return
	}
	adminOK(c, gin.H{
		"uuid":               user.UUID,
		"token_version":      user.TokenVersion,
		"connections_kicked": kickGatewayConnections(user.UUID),
	})
}

// kickGatewayConnections asks the gateway to drop the user's live
// connections. It is best-effort: with an unreachable gateway the revocation
// still stands and the connections die at their next reconnect.
func kickGatewayConnections(userUUID string) int {
	body, _ := json.Marshal(map[string]string{"user_uuid": userUUID})
	request, err := http.NewRequest(http.MethodPost, gatewayBaseURL()+"/internal/v1/kick", bytes.NewReader(body))
	if err != nil {
		return 0
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-IM-Internal-Secret", envOr("IM_INTERNAL_SECRET", ""))
	response, err := kickHTTPClient.Do(request)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0
	}
	var result struct {
		Kicked int `json:"kicked"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		return 0
	}
	return result.Kicked
}

func gatewayBaseURL() string {
	return strings.TrimRight(envOr("IM_GATEWAY_URL", "http://127.0.0.1:1910"), "/")
}
