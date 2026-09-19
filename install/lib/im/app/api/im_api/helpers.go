package im_api

import (
	"net/http"

	"github.com/daqing/airway-im-plugin/install/lib/im/app/auth"
	"github.com/daqing/airway-im-plugin/install/lib/im/app/models"
	"github.com/gin-gonic/gin"
)

func currentUser(c *gin.Context) (*models.User, bool) {
	user, err := auth.UserFromHeader(c.GetHeader("Authorization"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, 10000, "Could not authenticate user")
		return nil, false
	}
	if user == nil {
		respondError(c, http.StatusBadRequest, 10001, "Invalid bearer token")
		return nil, false
	}
	return user, true
}

func respond(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"code": 0, "data": data, "message": nil})
}

func respondError(c *gin.Context, status, code int, message string) {
	c.JSON(status, gin.H{"code": code, "data": nil, "message": message})
}
