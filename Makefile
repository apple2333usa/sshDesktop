# 构建：先构建前端（嵌入二进制），再构建 Go 程序。
VERSION := $(shell cat VERSION)
LDFLAGS := -s -w
export CGO_ENABLED := 0

.PHONY: all web build dist test test-go test-web e2e clean

all: build

web:
	cd web && npm ci && npm run build

build: web
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/sshdesktop ./cmd/sshdesktop

# 发布用：amd64 / arm64 二进制和 SHA256SUMS，输出到 dist/
dist: web
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sshdesktop-linux-amd64 ./cmd/sshdesktop
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sshdesktop-linux-arm64 ./cmd/sshdesktop
	cd dist && sha256sum sshdesktop-linux-* > SHA256SUMS

test: test-go test-web

test-go:
	go vet ./...
	go test ./...

test-web:
	cd web && npm run typecheck && npm test

# 对运行中的面板执行端到端测试：make e2e SD_USER=admin SD_PASS=xxx
e2e:
	cd web && npx playwright test

clean:
	rm -rf bin dist
	find web/dist -mindepth 1 ! -name .gitkeep -delete
