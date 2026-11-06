# AnonChat 常用命令。所有命令默认在项目根目录执行。
# 本机开发不依赖 Docker：测试与迁移通过 DATABASE_URL / TEST_DATABASE_URL 直接连接本机 PostgreSQL。
# 依赖 Docker 的目标（db-up）按方案照常提供，本机未安装 Docker 时可跳过。

SHELL := /bin/bash

BACKEND_DIR := backend
FRONTEND_DIR := frontend
DEPLOY_DIR := deploy

# 默认连接本机已运行的 PostgreSQL 16（密码认证下同样可用，本机 trust 认证时忽略密码）。
DATABASE_URL ?= postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_dev?sslmode=disable
TEST_DATABASE_URL ?= postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_test?sslmode=disable
export DATABASE_URL

.PHONY: help dev test test-integration web-test db-up db-down migrate migrate-down sqlc build lint vet fmt tidy e2e clean

help: ## 显示可用命令
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

dev: ## 启动后端开发服务（自动迁移）
	cd $(BACKEND_DIR) && go run ./cmd/server serve

test: ## 后端单元测试
	cd $(BACKEND_DIR) && go test ./...

test-integration: ## 后端集成测试（使用 TEST_DATABASE_URL，带 integration 构建标签）
	cd $(BACKEND_DIR) && TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -tags integration -p 1 ./...

web-test: ## 前端测试 + 类型检查 + 静态检查
	cd $(FRONTEND_DIR) && pnpm test && pnpm typecheck && pnpm lint

db-up: ## 启动开发数据库（依赖 Docker，按方案提供）
	cd $(DEPLOY_DIR) && docker compose -f docker-compose.dev.yml up -d postgres

db-down: ## 停止开发数据库
	cd $(DEPLOY_DIR) && docker compose -f docker-compose.dev.yml down

migrate: ## 执行数据库迁移（up）
	cd $(BACKEND_DIR) && DATABASE_URL='$(DATABASE_URL)' go run ./cmd/server migrate up

migrate-down: ## 回滚全部数据库迁移（down）
	cd $(BACKEND_DIR) && DATABASE_URL='$(DATABASE_URL)' go run ./cmd/server migrate down

sqlc: ## 用 sqlc 生成类型安全的查询代码
	cd $(BACKEND_DIR) && go tool sqlc generate

build: ## 构建后端二进制
	cd $(BACKEND_DIR) && go build ./...
	cd $(FRONTEND_DIR) && pnpm build

lint: ## 静态检查
	cd $(BACKEND_DIR) && go vet ./... && go tool staticcheck ./...
	cd $(FRONTEND_DIR) && pnpm lint

vet: ## 后端 go vet
	cd $(BACKEND_DIR) && go vet ./...

fmt: ## 格式化
	cd $(BACKEND_DIR) && gofmt -w .

tidy: ## 整理 go.mod
	cd $(BACKEND_DIR) && go mod tidy

e2e: ## Playwright E2E（任务 11.9 后可用）
	cd $(FRONTEND_DIR) && pnpm e2e

clean: ## 清理构建产物
	cd $(BACKEND_DIR) && go clean -cache -testcache
	rm -rf $(FRONTEND_DIR)/dist
