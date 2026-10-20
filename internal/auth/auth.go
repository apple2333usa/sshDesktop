// Package auth 负责登录、会话与防爆破。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"gorm.io/gorm"
)

type Service struct {
	st *store.Store

	mu       sync.Mutex
	throttle map[string]*throttleState // 本机来源（SSH 隧道）按用户名逐次延迟
	dummy    string
}

type throttleState struct {
	fails     int
	last      time.Time
	nextAllow time.Time
}

func New(st *store.Store) *Service {
	dummy, _ := HashPassword(RandomPassword(16))
	return &Service{st: st, throttle: map[string]*throttleState{}, dummy: dummy}
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Login 校验账号密码并创建会话。loopback 表示请求来自本机（SSH 隧道）。
func (s *Service) Login(username, password, ip, ua string, loopback bool) (string, *store.Session, *store.User, error) {
	username = strings.TrimSpace(username)
	rt := s.st.Runtime()
	now := time.Now()

	if loopback {
		s.mu.Lock()
		ts := s.throttle[username]
		if ts != nil && now.Before(ts.nextAllow) {
			wait := int(math.Ceil(ts.nextAllow.Sub(now).Seconds()))
			s.mu.Unlock()
			e := api.Err(http.StatusTooManyRequests, api.CodeRetryLater, fmt.Sprintf("登录失败次数过多，请 %d 秒后再试", wait))
			e.Data = map[string]any{"retryAfter": wait}
			return "", nil, nil, e
		}
		s.mu.Unlock()
	} else {
		var ban store.IPBan
		if err := s.st.DB.Where("ip = ? AND until > ?", ip, now).First(&ban).Error; err == nil {
			e := api.Err(http.StatusForbidden, api.CodeIPBanned, "该 IP 因多次登录失败已被暂时封禁，解封时间："+ban.Until.Local().Format("2006-01-02 15:04:05"))
			e.Data = map[string]any{"until": ban.Until}
			return "", nil, nil, e
		}
	}

	var u store.User
	err := s.st.DB.Where("username = ?", username).First(&u).Error
	ok := false
	if err == nil {
		ok = VerifyPassword(u.PasswordHash, password)
	} else {
		VerifyPassword(s.dummy, password) // 保持耗时一致，不泄露账号是否存在
	}
	if ok && u.Disabled {
		ok = false
	}
	s.st.DB.Create(&store.LoginAttempt{IP: ip, Username: username, Success: ok, At: now})

	if !ok {
		if loopback {
			s.mu.Lock()
			ts := s.throttle[username]
			if ts == nil || now.Sub(ts.last) > time.Duration(rt.BanWindowMinutes)*time.Minute {
				ts = &throttleState{}
				s.throttle[username] = ts
			}
			ts.fails++
			ts.last = now
			delay := math.Min(math.Pow(2, float64(ts.fails-1)), 30)
			ts.nextAllow = now.Add(time.Duration(delay * float64(time.Second)))
			s.mu.Unlock()
		} else {
			var n int64
			s.st.DB.Model(&store.LoginAttempt{}).
				Where("ip = ? AND success = ? AND at > ?", ip, false, now.Add(-time.Duration(rt.BanWindowMinutes)*time.Minute)).
				Count(&n)
			if int(n) >= rt.BanMaxFailures {
				s.st.DB.Save(&store.IPBan{IP: ip, Reason: fmt.Sprintf("%d 分钟内登录失败 %d 次", rt.BanWindowMinutes, n),
					Until: now.Add(time.Duration(rt.BanMinutes) * time.Minute), CreatedAt: now})
			}
		}
		return "", nil, nil, api.Err(http.StatusUnauthorized, api.CodeBadCredentials, "用户名或密码错误")
	}

	if loopback {
		s.mu.Lock()
		delete(s.throttle, username)
		s.mu.Unlock()
	}
	token := newToken()
	sess := store.Session{TokenHash: hashToken(token), UserID: u.ID, IP: ip, UserAgent: ua, CreatedAt: now, LastSeenAt: now}
	if err := s.st.DB.Create(&sess).Error; err != nil {
		return "", nil, nil, err
	}
	s.st.DB.Model(&u).Updates(map[string]any{"last_login_at": now, "last_login_ip": ip})
	return token, &sess, &u, nil
}


var ErrNoSession = errors.New("未登录或会话已过期")

// Authenticate 根据令牌查找会话，检查空闲超时并刷新最后活动时间。
func (s *Service) Authenticate(token string) (*store.Session, *store.User, error) {
	if token == "" {
		return nil, nil, ErrNoSession
	}
	var sess store.Session
	if err := s.st.DB.Where("token_hash = ?", hashToken(token)).First(&sess).Error; err != nil {
		return nil, nil, ErrNoSession
	}
	rt := s.st.Runtime()
	now := time.Now()
	if now.Sub(sess.LastSeenAt) > time.Duration(rt.SessionTimeoutHours)*time.Hour {
		s.st.DB.Delete(&sess)
		return nil, nil, ErrNoSession
	}
	var u store.User
	if err := s.st.DB.First(&u, sess.UserID).Error; err != nil || u.Disabled {
		s.st.DB.Delete(&sess)
		return nil, nil, ErrNoSession
	}
	if now.Sub(sess.LastSeenAt) > time.Minute {
		s.st.DB.Model(&sess).Update("last_seen_at", now)
	}
	return &sess, &u, nil
}

func (s *Service) Logout(token string) {
	s.st.DB.Where("token_hash = ?", hashToken(token)).Delete(&store.Session{})
}

// RevokeUser 注销某账号的所有会话（改密码、停用账号时调用）。exceptSession 非 0 时保留该会话。
func (s *Service) RevokeUser(userID, exceptSession uint) {
	q := s.st.DB.Where("user_id = ?", userID)
	if exceptSession != 0 {
		q = q.Where("id <> ?", exceptSession)
	}
	q.Delete(&store.Session{})
}

func (s *Service) RevokeSession(id uint) { s.st.DB.Delete(&store.Session{}, id) }

// CheckPassword 用于二级危险操作的再次输入密码确认。
func (s *Service) CheckPassword(userID uint, pw string) bool {
	var u store.User
	if err := s.st.DB.First(&u, userID).Error; err != nil {
		return false
	}
	return VerifyPassword(u.PasswordHash, pw)
}

// SetPassword 修改密码并注销该账号其他会话。
func (s *Service) SetPassword(userID uint, pw string, keepSession uint) error {
	if err := CheckPasswordStrength(pw); err != nil {
		return api.Err(http.StatusBadRequest, api.CodeWeakPassword, err.Error())
	}
	h, err := HashPassword(pw)
	if err != nil {
		return err
	}
	if err := s.st.DB.Model(&store.User{}).Where("id = ?", userID).Update("password_hash", h).Error; err != nil {
		return err
	}
	s.RevokeUser(userID, keepSession)
	return nil
}

// Cleanup 清理过期会话、登录记录和封禁。
func (s *Service) Cleanup() {
	rt := s.st.Runtime()
	now := time.Now()
	s.st.DB.Where("last_seen_at < ?", now.Add(-time.Duration(rt.SessionTimeoutHours)*time.Hour)).Delete(&store.Session{})
	s.st.DB.Where("at < ?", now.AddDate(0, 0, -30)).Delete(&store.LoginAttempt{})
	s.st.DB.Where("until < ?", now).Delete(&store.IPBan{})
	s.mu.Lock()
	for k, v := range s.throttle {
		if now.Sub(v.last) > time.Hour {
			delete(s.throttle, k)
		}
	}
	s.mu.Unlock()
}

// CreateUser 创建账号；roleID 为 0 时使用管理员角色。
func CreateUser(db *gorm.DB, username, password string, roleID uint) (*store.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 || strings.ContainsAny(username, " \t\r\n") {
		return nil, api.BadRequest("用户名不能为空、不能包含空白字符，最长 64 个字符")
	}
	if err := CheckPasswordStrength(password); err != nil {
		return nil, api.Err(http.StatusBadRequest, api.CodeWeakPassword, err.Error())
	}
	var n int64
	db.Model(&store.User{}).Where("username = ?", username).Count(&n)
	if n > 0 {
		return nil, api.Conflict(api.CodeUserExists, "用户名已存在")
	}
	h, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &store.User{Username: username, PasswordHash: h, RoleID: roleID}
	return u, db.Create(u).Error
}
