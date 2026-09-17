package im_api

import "github.com/gin-gonic/gin"

func Routes(r *gin.RouterGroup) {
	r.POST("/group", CreateGroup)
	r.GET("/conversations", ListConversations)
	r.POST("/conversations", CreateConversation)
	r.GET("/conversations/:conversation_uuid", GetConversation)
	r.POST("/conversations/:conversation_uuid/members", AddMembers)
	r.DELETE("/conversations/:conversation_uuid/members/:user_id", RemoveMember)
	r.GET("/conversations/:conversation_uuid/messages", ListMessages)
	r.POST("/conversations/:conversation_uuid/messages", CreateConversationMessage)
	r.POST("/messages", CreateMessage)
}
