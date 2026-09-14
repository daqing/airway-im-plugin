package admin_api

import "github.com/gin-gonic/gin"

func adminOK(c *gin.Context, data any) {
	c.JSON(200, gin.H{"code": 0, "data": data, "message": nil})
}

func adminError(c *gin.Context, status, code int, message string) {
	c.JSON(status, gin.H{"code": code, "data": nil, "message": message})
}
