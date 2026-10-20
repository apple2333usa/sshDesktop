// Package api 提供统一响应格式、错误码和请求上下文工具，供各模块的 handler 使用。
package api

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// 错误码按模块分段：1xxx 通用，2xxx 登录与账号，3xxx 文件，4xxx 终端，5xxx 进程与服务。
const (
	CodeOK              = 0
	CodeBadRequest      = 1001
	CodeUnauthorized    = 1002
	CodeForbidden       = 1003
	CodeNotFound        = 1004
	CodeConflict        = 1005
	CodeTooMany         = 1006
	CodeConfirmRequired = 1007
	CodeConfirmFailed   = 1008
	CodeInternal        = 1500

	CodeBadCredentials = 2001
	CodeIPBanned       = 2002
	CodeRetryLater     = 2003
	CodeLastAdmin      = 2004
	CodeUserExists     = 2005
	CodeWeakPassword   = 2006

	CodeFileNotExist     = 3001
	CodeFileExists       = 3002
	CodeFileModified     = 3003
	CodeNotDir           = 3004
	CodeTrashUnavailable = 3005
	CodeFileTooLarge     = 3006
	CodeBinaryFile       = 3007
	CodeUnsupported      = 3008

	CodeSessionGone = 4001

	CodeCommandFailed = 5001
)

type Error struct {
	Status int
	Code   int
	Msg    string
	Data   any
}

func (e *Error) Error() string { return e.Msg }

func Err(status, code int, msg string) *Error { return &Error{Status: status, Code: code, Msg: msg} }

func BadRequest(msg string) *Error { return Err(http.StatusBadRequest, CodeBadRequest, msg) }
func NotFound(msg string) *Error   { return Err(http.StatusNotFound, CodeNotFound, msg) }
func Forbidden(msg string) *Error  { return Err(http.StatusForbidden, CodeForbidden, msg) }
func Conflict(code int, msg string) *Error {
	return Err(http.StatusConflict, code, msg)
}

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: CodeOK, Data: data})
}

// Fail 输出错误响应，并把错误信息留给审计中间件。
func Fail(c *gin.Context, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = FromOS(err)
	}
	c.Set(ctxAuditErr, e.Msg)
	if e.Status >= 500 {
		slog.Error("请求失败", "path", c.Request.URL.Path, "err", err)
	}
	c.AbortWithStatusJSON(e.Status, Response{Code: e.Code, Message: e.Msg, Data: e.Data})
}

// FromOS 把常见的文件系统错误转换为带错误码的 Error。
func FromOS(err error) *Error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Err(http.StatusNotFound, CodeFileNotExist, "文件或目录不存在")
	case errors.Is(err, fs.ErrExist):
		return Err(http.StatusConflict, CodeFileExists, "目标已存在")
	case errors.Is(err, fs.ErrPermission):
		return Err(http.StatusForbidden, CodeForbidden, "没有权限："+err.Error())
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return Err(http.StatusBadRequest, CodeBadRequest, err.Error())
	}
	return Err(http.StatusInternalServerError, CodeInternal, err.Error())
}

// Bind 解析 JSON 请求体，失败时直接输出 400 并返回 false。
func Bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		Fail(c, BadRequest("请求参数错误："+err.Error()))
		return false
	}
	return true
}

// Principal 是当前登录的账号。
type Principal struct {
	UserID    uint
	Username  string
	RoleID    uint
	SessionID uint
}

const (
	ctxUser      = "sd.user"
	ctxIP        = "sd.ip"
	ctxAuditErr  = "sd.audit.err"
	ctxAuditTgt  = "sd.audit.target"
	ctxAuditNode = "sd.audit.node"
)

func SetUser(c *gin.Context, p *Principal) { c.Set(ctxUser, p) }

func User(c *gin.Context) *Principal {
	if v, ok := c.Get(ctxUser); ok {
		return v.(*Principal)
	}
	return nil
}

func SetIP(c *gin.Context, ip string) { c.Set(ctxIP, ip) }
func IP(c *gin.Context) string        { return c.GetString(ctxIP) }

// Target 记录本次操作的对象（文件路径、进程号等），写入审计日志。
func Target(c *gin.Context, t string)   { c.Set(ctxAuditTgt, t) }
func AuditTarget(c *gin.Context) string { return c.GetString(ctxAuditTgt) }
func AuditErr(c *gin.Context) string    { return c.GetString(ctxAuditErr) }

func SetNode(c *gin.Context, n string) { c.Set(ctxAuditNode, n) }
func Node(c *gin.Context) string       { return c.GetString(ctxAuditNode) }
