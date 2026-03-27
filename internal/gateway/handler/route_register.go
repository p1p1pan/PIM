package handler

import (
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	authhandler "pim/internal/auth/handler"
)

// RegisterRoutes 注册 Gateway 对外路由。
// 为提高可读性，按能力域拆成多个 registerXxxRoutes。
func (s *HTTPServer) RegisterRoutes(r *gin.Engine) {
	authGroup := r.Group("/api/v1", authhandler.GRPCMiddleware(s.authClient))
	publicGroup := r.Group("/api/v1")

	r.GET("/ws", authhandler.GRPCMiddleware(s.authClient), s.handleWS)
	r.POST("/api/v1/login", s.handleLogin)
	r.POST("/api/v1/register", s.handleRegister)

	s.registerUserAndFriendRoutes(authGroup)
	s.registerConversationRoutes(authGroup)
	s.registerGroupRoutes(authGroup)
	s.registerFileRoutes(authGroup)
	s.registerLogRoutes(publicGroup)
	s.registerPublicAdminRoutes(publicGroup)
	s.registerAdminRoutes(authGroup)
	s.captureAPIRouteCatalog(r)
}

func (s *HTTPServer) captureAPIRouteCatalog(r *gin.Engine) {
	seen := make(map[string]struct{})
	routes := make([]string, 0)
	for _, ri := range r.Routes() {
		path := strings.TrimSpace(ri.Path)
		if path == "" {
			continue
		}
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		routes = append(routes, path)
	}
	sort.Strings(routes)
	s.apiRouteCatalog = routes
}

func (s *HTTPServer) registerUserAndFriendRoutes(authGroup *gin.RouterGroup) {
	authGroup.GET("/me", s.handleMe)
	authGroup.GET("/friends", s.handleListFriends)
	authGroup.POST("/friends/requests", s.handleSendFriendRequest)
	authGroup.POST("/friends/requests/:id/approve", s.handleApproveFriendRequest)
	authGroup.POST("/friends/requests/:id/reject", s.handleRejectFriendRequest)
	authGroup.DELETE("/friends/:user_id", s.handleDeleteFriend)
	authGroup.GET("/friends/requests/incoming", s.handleListIncomingFriendRequests)
	authGroup.GET("/friends/requests/outgoing", s.handleListOutgoingFriendRequests)
}

func (s *HTTPServer) registerConversationRoutes(authGroup *gin.RouterGroup) {
	authGroup.GET("/conversations", s.handleListConversations)
	authGroup.GET("/messages", s.handleListMessages)
	authGroup.PUT("/conversations/:peer_id/read", s.handleConversationRead)
}

func (s *HTTPServer) registerGroupRoutes(authGroup *gin.RouterGroup) {
	authGroup.POST("/groups", s.handleCreateGroup)
	authGroup.GET("/groups", s.handleListMyGroups)
	authGroup.GET("/groups/conversations", s.handleListGroupConversations)
	authGroup.GET("/groups/:id", s.handleGetGroup)
	authGroup.PUT("/groups/:id", s.handleUpdateGroup)
	authGroup.POST("/groups/:id/leave", s.handleLeaveGroup)
	authGroup.POST("/groups/:id/disband", s.handleDisbandGroup)
	authGroup.POST("/groups/:id/transfer-owner", s.handleTransferGroupOwner)
	authGroup.POST("/groups/:id/members", s.handleAddGroupMember)
	authGroup.DELETE("/groups/:id/members/:user_id", s.handleRemoveGroupMember)
	authGroup.GET("/groups/:id/members", s.handleListGroupMembers)
	authGroup.POST("/groups/:id/messages", s.handleSendGroupMessage)
	authGroup.GET("/groups/:id/messages", s.handleListGroupMessages)
	authGroup.PUT("/groups/:id/read", s.handleMarkGroupRead)
}

func (s *HTTPServer) registerFileRoutes(authGroup *gin.RouterGroup) {
	authGroup.POST("/files/prepare", s.handleFilePrepare)
	authGroup.POST("/files/:id/commit", s.handleFileCommit)
	authGroup.GET("/files/:id", s.handleFileGet)
}

func (s *HTTPServer) registerLogRoutes(publicGroup *gin.RouterGroup) {
	publicGroup.GET("/logs/trace/:trace_id", s.handleLogsByTrace)
	publicGroup.GET("/logs/search", s.handleLogsByEvent)
	publicGroup.GET("/logs/filter", s.handleLogsFilter)
}

func (s *HTTPServer) registerAdminRoutes(authGroup *gin.RouterGroup) {
	authGroup.POST("/admin/observability/drill/http-500", s.handleAdminDrillHTTP500)
	authGroup.GET("/admin/observability/drill/latency", s.handleAdminDrillLatency)
	authGroup.GET("/admin/file-scan/dlq", s.handleFileScanDLQList)
	authGroup.POST("/admin/file-scan/dlq/:file_id/replay", s.handleFileScanDLQReplay)
}

// registerPublicAdminRoutes 暴露只读监督接口，便于无需登录即可查看运行态。
// 写操作（如 drill、DLQ replay）仍放在鉴权路由，避免被匿名调用。
func (s *HTTPServer) registerPublicAdminRoutes(publicGroup *gin.RouterGroup) {
	publicGroup.GET("/admin/health", s.handleAdminHealth)
	publicGroup.GET("/admin/metrics", s.handleAdminMetrics)
	publicGroup.GET("/admin/observability/overview", s.handleAdminObservabilityOverview)
}
