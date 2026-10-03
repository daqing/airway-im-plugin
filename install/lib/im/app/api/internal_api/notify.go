package internal_api

import (
	"net/http"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/notify"
	"github.com/gin-gonic/gin"
)

type notifyRequest struct {
	UserUUIDs []string       `json:"user_uuids"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
}

// Notify queues a host-domain notification onto the IM delivery pipeline.
// Third-party host backends (non-Go ones reach this over HTTP with the
// internal secret) use it to push events like friend requests or mail
// alerts to specific users over the same WebSocket gateway chat uses.
// Delivery is advisory: online recipients get the frame in real time;
// offline recipients are not replayed, so hosts must pair notifications
// with a pull endpoint.
func Notify(c *gin.Context) {
	if !internalAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 10005, "data": nil, "message": "Internal authentication required"})
		return
	}

	var p notifyRequest
	if err := c.BindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 10003, "data": nil, "message": "Invalid JSON request"})
		return
	}

	eventID, err := notify.Emit(p.UserUUIDs, p.Event, p.Data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 10003, "data": nil, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"event_id": eventID}, "message": nil})
}
