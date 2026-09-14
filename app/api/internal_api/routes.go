package internal_api

import "github.com/gin-gonic/gin"

func Routes(r *gin.Engine) {
	group := r.Group("/internal/v1")
	group.GET("/auth", Authenticate)
	group.POST("/credentials", IssueCredential)
	group.GET("/outbox", Outbox)
	group.POST("/outbox/:id/ack", AckOutbox)
}
