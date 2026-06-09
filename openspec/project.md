# Project Conventions

## 技术栈（实现约束）

- **语言**：Go (1.26)。
- **Web 框架**：**Echo (github.com/labstack/echo/v4)** —— HTTP 接入层统一用 Echo 编写，不直接裸用 net/http 路由。
- **存储**：SQLite（纯 Go 驱动 `modernc.org/sqlite`，免 CGO），兼作投递工作队列（MVP）。
- **MQ**：进程内 pub/sub（MVP 状态事件分发的轻量替身，抽象保留以便后续换真 MQ）。
- **凭证**：本地加密配置文件 / 环境变量（sops 的轻量替身），经 `secret.Resolver` 抽象，可后换 Vault/KMS。

## 结构

- `cmd/server` —— 入口与装配。
- `internal/` —— 分层：`api`(Echo) / `ingest` / `deliver` / `adapter` / `store` / `secret` / `mq` / `observ` / `model` / `config` / `metrics` / `id`。

## 备注

- MVP 取向：可跑通主流程优先，不追求生产级齐全；重型基础设施用接口 + 轻量实现替身。
- 详细设计与取舍见 `docs/` 与 `openspec/changes/notification-delivery-gateway/`。
