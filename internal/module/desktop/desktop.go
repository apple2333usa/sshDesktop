// Package desktop 保存每个账号的桌面状态（打开的窗口、图标排列、壁纸、主题等）。
package desktop

import (
	"encoding/json"
	"io"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

const maxState = 1 << 20

type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

func (s *Service) Register(r *gin.RouterGroup, login gin.HandlerFunc) {
	r.GET("/desktop/state", login, s.get)
	r.PUT("/desktop/state", login, s.put)
}

func (s *Service) get(c *gin.Context) {
	var ds store.DesktopState
	if err := s.st.DB.First(&ds, "user_id = ?", api.User(c).UserID).Error; err != nil {
		api.OK(c, nil)
		return
	}
	api.OK(c, json.RawMessage(ds.State))
}

func (s *Service) put(c *gin.Context) {
	b, err := io.ReadAll(io.LimitReader(c.Request.Body, maxState+1))
	if err != nil || len(b) > maxState || !json.Valid(b) {
		api.Fail(c, api.BadRequest("桌面状态无效或超过 1 MB"))
		return
	}
	ds := store.DesktopState{UserID: api.User(c).UserID, State: string(b), UpdatedAt: time.Now()}
	if err := s.st.DB.Save(&ds).Error; err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}
