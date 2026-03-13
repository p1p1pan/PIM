package main

import (
    "fmt"
    "log"
    "net/http"
	"strings"
	"sync"
	"errors"

    "github.com/gin-gonic/gin"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"

    "pim/internal/config"
    "pim/internal/user"
	"pim/internal/chat"
	"pim/internal/friend"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

// WebSocket 相关变量
var (
    upgrader = websocket.Upgrader{
        CheckOrigin: func(r *http.Request) bool {
            return true // 开发阶段先全放行，生产要限制
        },
    }
    // 在线连接表：userID -> conn
    wsConnections = make(map[uint]*websocket.Conn)
    wsMu          sync.RWMutex
)


func main() {
	r := gin.Default()
	// 从 internal/config/config.go 获取配置信息
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.DBHost,
		config.DBPort,
		config.DBUser,
		config.DBPassword,
		config.DBName,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Println("Connected to database")

	// 迁移数据库表
	if err := db.AutoMigrate(&user.User{}, &chat.Message{}, &friend.Friend{}); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	// 受保护路由组 获取当前用户信息
	authGroup := r.Group("/api/v1", AuthMiddleware())
	user.RegisterRoutes(r, authGroup, db)
	friend.RegisterRoutes(r, authGroup, db)
	chat.RegisterRoutes(r, authGroup, db)

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "ok",
		})
	})

	// WebSocket 路由
	r.GET("/ws", AuthMiddleware(),func(c *gin.Context) {
		// 获取 userID
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)

		// 升级连接
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("Failed to upgrade to WebSocket: %v", err)
			return
		}
		// 记录连接
		wsMu.Lock()
		wsConnections[userID] = conn
		wsMu.Unlock()
		log.Printf("User %d connected to WebSocket", userID)

		// 读取消息循环
		for {	
			var msg struct {
				To uint `json:"to"`
				Content string `json:"content"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				log.Printf("Failed to read JSON: %v", err)
				break
			}
			
			// 检查是否好友
			var friend friend.Friend
			if err := db.Where("user_id = ? AND friend_id = ?", userID, msg.To).First(&friend).Error; err != nil {
				// 如果好友不存在，则发送错误提示，不转发
				if errors.Is(err, gorm.ErrRecordNotFound)	 {
					_ = conn.WriteJSON(gin.H{
						"error": "not friends, cannot send message",
						"to":    msg.To,
					})
					continue
				}
				log.Printf("Failed to check if user %d is friend of user %d: %v", userID, msg.To, err)
				continue
			}
			
			// 保存消息到数据库
			m := chat.Message{
				FromUserID: userID,
				ToUserID: msg.To,
				Content: msg.Content,
			}
			if err := db.Create(&m).Error; err != nil {
				log.Printf("Failed to create message: %v", err)
				break
			}

			// 找到接收方连接并发送
			wsMu.RLock()
			toConn := wsConnections[msg.To]
			wsMu.RUnlock()

			if toConn != nil {
				out := struct {
					From uint `json:"from"`
					Content string `json:"content"`
				}{
					From: userID,
					Content: msg.Content,
				}
				if err := toConn.WriteJSON(out); err != nil {
					log.Printf("Failed to send message: %v", err)
					break
				}
			}
			log.Printf("Message sent to user %d", msg.To)
		}


		// 清理
		wsMu.Lock()
		delete(wsConnections, userID)
		wsMu.Unlock()
		conn.Close()
	})

	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	log.Println("Server is running on port 8080")

}

// 鉴权中间件
func AuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
		// 获取 Authorization 头
        authHeader := c.GetHeader("Authorization")
		// 如果 Authorization 头为空或者不是 Bearer 开头
        if authHeader == "" {
			tokenFromQuery := c.Query("token")
			if tokenFromQuery != "" {
				authHeader = "Bearer " + tokenFromQuery
			}
		}
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid token"})
			return
		}
		// 去掉 Bearer 前缀
        tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		// 解析 token 使用密钥
        token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
            if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
                return nil, fmt.Errorf("unexpected signing method")
            }
            return []byte(config.JWTSecret), nil
        })
		// 如果解析失败或者 token 无效
        if err != nil || !token.Valid {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
            return
        }
		// 获取 claims
        claims, ok := token.Claims.(jwt.MapClaims)
        if !ok {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token claims"})
            return
        }

        // 从 claims 取出 user_id，存到 context
        c.Set("userID", uint(claims["sub"].(float64))) // 注意类型断言

        c.Next()
    }
}