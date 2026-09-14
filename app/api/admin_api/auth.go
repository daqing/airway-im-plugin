package admin_api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const sessionDuration = 12 * time.Hour

var adminSessions = struct {
	sync.Mutex
	values map[string]time.Time
}{values: make(map[string]time.Time)}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func Login(c *gin.Context) {
	var request loginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		adminError(c, http.StatusBadRequest, 20003, "Invalid login request")
		return
	}
	wantUsername := strings.TrimSpace(os.Getenv("IM_ADMIN_USERNAME"))
	wantPassword := os.Getenv("IM_ADMIN_PASSWORD")
	if wantUsername == "" || wantPassword == "" {
		adminError(c, http.StatusServiceUnavailable, 20006, "Admin login is not configured")
		return
	}
	if !secureEqual(request.Username, wantUsername) || !secureEqual(request.Password, wantPassword) {
		adminError(c, http.StatusUnauthorized, 20001, "Invalid administrator credentials")
		return
	}
	token, err := randomToken()
	if err != nil {
		adminError(c, http.StatusInternalServerError, 20000, "Could not create admin session")
		return
	}
	expiresAt := time.Now().UTC().Add(sessionDuration)
	adminSessions.Lock()
	adminSessions.values[token] = expiresAt
	adminSessions.Unlock()
	adminOK(c, gin.H{"token": token, "expires_at": expiresAt, "username": wantUsername})
}

func Logout(c *gin.Context) {
	token, _ := adminBearer(c.GetHeader("Authorization"))
	adminSessions.Lock()
	delete(adminSessions.values, token)
	adminSessions.Unlock()
	adminOK(c, "OK")
}

func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := adminBearer(c.GetHeader("Authorization"))
		if !ok || !validSession(token) {
			adminError(c, http.StatusUnauthorized, 20002, "Administrator authentication required")
			c.Abort()
			return
		}
		c.Next()
	}
}

func validSession(token string) bool {
	now := time.Now().UTC()
	adminSessions.Lock()
	defer adminSessions.Unlock()
	for value, expiry := range adminSessions.values {
		if !expiry.After(now) {
			delete(adminSessions.values, value)
		}
	}
	expiry, exists := adminSessions.values[token]
	return exists && expiry.After(now)
}

func adminBearer(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func secureEqual(got, want string) bool {
	return len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func randomToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
