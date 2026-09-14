package admin_api

import "github.com/gin-gonic/gin"

func Routes(r *gin.Engine) {
	group := r.Group("/admin/api")
	group.POST("/login", Login)

	protected := group.Group("")
	protected.Use(requireAdmin())
	protected.POST("/logout", Logout)
	protected.GET("/status", Status)
	protected.GET("/users", ListUsers)
	protected.POST("/users/:user_uuid/revoke", RevokeUserCredentials)
	protected.GET("/conversations", ListConversations)
	protected.GET("/conversations/:conversation_uuid/messages", ListConversationMessages)
	protected.POST("/messages/:message_uuid/mark-illegal", MarkMessageIllegal)
}
