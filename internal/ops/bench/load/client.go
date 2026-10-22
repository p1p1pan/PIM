// Package benchload 为 test/bench/* 压测工具提供网关 HTTP/WS 公共逻辑（不 import internal 业务包）。
package benchload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// HTTPClient 专用于压测：提高对单网关 host 的并发 HTTP（登录/注册）能力，避免 http.DefaultClient 默认 Transport 限制导致大量失败。
var HTTPClient = func() *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        16384,
			MaxIdleConnsPerHost: 16384,
			// 避免无限制并发建连在压测瞬间触发连接拒绝风暴。
			MaxConnsPerHost: 512,
			IdleConnTimeout:     90 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}()

// Register 调用 POST /api/v1/register。若用户名已存在，返回 ErrUserExists。
func Register(ctx context.Context, base, username, password string) error {
	base = strings.TrimRight(base, "/")
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/register", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	// user-service 对「用户名已存在」可能映射为 400 或 500，靠文案识别。
	if strings.Contains(string(b), "already exists") {
		return ErrUserExists
	}
	return fmt.Errorf("register %s: %s", resp.Status, string(b))
}

// ErrUserExists 表示用户名已注册，可跳过。
var ErrUserExists = errors.New("username already exists")

// Login POST /api/v1/login，返回 JWT 与用户 ID。
func Login(ctx context.Context, base, username, password string) (token string, userID uint, err error) {
	base = strings.TrimRight(base, "/")
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/login", strings.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("login %s: %s", resp.Status, string(b))
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			ID uint `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", 0, err
	}
	if out.Token == "" || out.User.ID == 0 {
		return "", 0, fmt.Errorf("login response missing token or user id")
	}
	return out.Token, out.User.ID, nil
}

// SendFriendRequest 调用 POST /api/v1/friends/requests，返回 request_id（供 ApproveFriendRequest）。
func SendFriendRequest(ctx context.Context, base, token string, toUserID uint) (uint64, error) {
	base = strings.TrimRight(base, "/")
	body := fmt.Sprintf(`{"to_user_id":%d}`, toUserID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/friends/requests", strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("send friend request: %s: %s", resp.Status, string(b))
	}
	var out struct {
		RequestID uint64 `json:"request_id"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return 0, fmt.Errorf("parse send friend response: %w body=%s", err, string(b))
	}
	if out.RequestID == 0 {
		return 0, fmt.Errorf("missing request_id in %s", string(b))
	}
	return out.RequestID, nil
}

// ApproveFriendRequest 调用 POST /api/v1/friends/requests/:id/approve（需「被申请人」的 token）。
func ApproveFriendRequest(ctx context.Context, base, token string, requestID uint64) error {
	base = strings.TrimRight(base, "/")
	u := fmt.Sprintf("%s/api/v1/friends/requests/%d/approve", base, requestID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("approve friend request: %s: %s", resp.Status, string(b))
	}
	return nil
}

// HasFriend 查询当前用户好友列表中是否已包含对方 user.id（与 ListFriends 中 friend_id 一致）。
func HasFriend(ctx context.Context, base, token string, peerUserID uint) (bool, error) {
	base = strings.TrimRight(base, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/friends", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("list friends: %s: %s", resp.Status, string(b))
	}
	var out struct {
		Friends []struct {
			FriendID uint `json:"friend_id"`
		} `json:"friends"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return false, err
	}
	for _, f := range out.Friends {
		if f.FriendID == peerUserID {
			return true, nil
		}
	}
	return false, nil
}

// HTTPToWS 将 http(s) 网关地址转为 ws(s) 的 /ws 路径（不含 query）。
func HTTPToWS(httpBase string) string {
	u, err := url.Parse(httpBase)
	if err != nil {
		return "ws://" + strings.TrimPrefix(httpBase, "http://") + "/ws"
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	host := u.Host
	if host == "" {
		host = u.Path
	}
	return scheme + "://" + host + "/ws"
}

// dialer 用于压测：略拉长握手超时，与 net.Dialer 一致策略。
var dialer = websocket.Dialer{
	HandshakeTimeout: 30 * time.Second,
	NetDialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
}

// DialWS 使用 token query 建立 WebSocket。
func DialWS(wsBaseURL, token string) (*websocket.Conn, error) {
	u, err := url.Parse(wsBaseURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	conn, _, err := dialer.Dial(u.String(), nil)
	return conn, err
}

// LoginWithRetries 仅登录，失败重试若干次，返回首次成功拿到的 token。
func LoginWithRetries(ctx context.Context, gatewayHTTP, prefix, password string, userNum int, retries int) (token string, err error) {
	user := fmt.Sprintf("%s%d", prefix, userNum)
	var lastErr error
	for r := 0; r < retries; r++ {
		tok, _, err := Login(ctx, gatewayHTTP, user, password)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(30+r*25) * time.Millisecond)
			continue
		}
		return tok, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unknown login failure user=%s", user)
	}
	return "", fmt.Errorf("after %d retries: %w", retries, lastErr)
}

// DialWSWithRetries 仅 WebSocket 握手，失败重试若干次。
func DialWSWithRetries(wsBase, token string, retries int) (*websocket.Conn, error) {
	var lastErr error
	for r := 0; r < retries; r++ {
		c, err := DialWS(wsBase, token)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(30+r*25) * time.Millisecond)
			continue
		}
		return c, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dial ws failed")
	}
	return nil, fmt.Errorf("after %d retries: %w", retries, lastErr)
}

// LoginDialWS 登录并建连，失败时重试若干次（适合万级压测下的瞬时失败）。
func LoginDialWS(ctx context.Context, gatewayHTTP, wsBase, prefix, password string, userNum int, retries int) (*websocket.Conn, error) {
	user := fmt.Sprintf("%s%d", prefix, userNum)
	var lastErr error
	for r := 0; r < retries; r++ {
		tok, _, err := Login(ctx, gatewayHTTP, user, password)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(30+r*25) * time.Millisecond)
			continue
		}
		c, err := DialWS(wsBase, tok)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(30+r*25) * time.Millisecond)
			continue
		}
		return c, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unknown failure user=%s", user)
	}
	return nil, fmt.Errorf("after %d retries: %w", retries, lastErr)
}

// DiscardReads 在后台读走下行，避免对端推送或控制帧堆积。
func DiscardReads(c *websocket.Conn) {
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
	}
}
