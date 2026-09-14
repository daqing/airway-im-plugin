package me_api

import (
	"net/http"

	"github.com/daqing/airway-im-plugin/app/auth"
	"github.com/gin-gonic/gin"
)

const invalidBearerTokenCode = 10001

// MeAction returns the user identified by the client's signed credential.
func MeAction(c *gin.Context) {
	user, err := auth.UserFromHeader(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    10000,
			"data":    nil,
			"message": "Could not load user",
		})
		return
	}
	if user == nil {
		invalidBearerToken(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"data":    user,
		"message": nil,
	})
}

func invalidBearerToken(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"code":    invalidBearerTokenCode,
		"data":    nil,
		"message": "Invalid bearer token",
	})
}
