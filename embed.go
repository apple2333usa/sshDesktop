// Package sshdesktop 只负责把前端构建产物和版本号嵌入二进制。
package sshdesktop

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed VERSION
var versionFile string

//go:embed all:web/dist
var webDist embed.FS

// Version 返回 VERSION 文件中的版本号。
func Version() string { return strings.TrimSpace(versionFile) }

// WebFS 返回前端构建产物（web/dist）。
func WebFS() fs.FS {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		panic(err)
	}
	return sub
}
