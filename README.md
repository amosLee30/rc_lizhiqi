# rc_lizhiqi · API 通知系统（出站通知可靠投递网关）

企业内部多个业务系统在关键事件（注册引流、订阅付款、下单等）发生时，需要调用外部供应商的 HTTPS API 进行通知。本服务作为**出站通知可靠投递网关**（适配中心模式）：业务方提交「通知类型 + 业务参数」，网关 fire-and-forget 地**尽可能可靠地送达**外部供应商，并把供应商对接知识（参数校验、请求组装、鉴权/验签）收敛到平台一层。

> 本仓库是作业的最小可行实现（MVP）。完整的需求分析、逐项取舍论证与架构图见 [`docs/`](docs/)。

---

## 快速开始

环境：Go 1.26+（纯 Go 依赖，免 CGO）。

```bash
# 1) 运行（默认监听 :8080，使用 suppliers.json / secrets.json）
SUPPLIERS_FILE=suppliers.json AD_TOKEN=ad-tok CRM_TOKEN=crm-tok go run ./cmd/server

# 2) 正式构建（gofmt/vet/test 检查 + 版本信息打入 -> bin/notify-server）
bash scripts/build.sh
GOOS=linux GOARCH=amd64 bash scripts/build.sh   # 交叉编译

# 3) 一键端到端演示（拉起假上游 + 网关，提交→投递→查询→指标）
bash scripts/smoke.sh

# 4) 测试
go test ./...            # 全部单测 + 集成测试
go test -race ./...      # 竞态检测
```

### 试一下

```bash
# 提交一条通知，拿到唯一追踪 ID
curl -X POST localhost:8080/notifications -H 'Content-Type: application/json' -d '{
  "idempotency_key":"order-1","source_system":"billing",
  "type":"crm-contact","params":{"contactId":"42","status":"paid"}
}'
# => {"tracking_id":"019e...-...","status":"ACCEPTED","duplicate":false}

# 凭追踪 ID 查询（粗三态）
curl localhost:8080/notifications/<tracking_id>
# 运维 detail 视图（含状态历史/重试/报错）
curl -H 'Authorization: Bearer ops-secret' 'localhost:8080/notifications/<tracking_id>?detail=true'
```

---

## API

| 方法 & 路径 | 说明 | 鉴权 |
|---|---|---|
| `POST /notifications` | 提交通知，返回唯一追踪 ID（幂等重提返回同一 ID + 200） | — |
| `GET /notifications/{id}` | 查询投递状态；默认粗三态，`?detail=true` 返回执行明细 | detail 需 ops |
| `GET /admin/dead` | 列出死信通知 | ops |
| `POST /admin/notifications/{id}/replay` | 重放死信（用 params + 最新适配类重组） | ops |
| `GET /healthz` | 健康检查 | — |
| `GET /metrics` | 计数指标（受理/投递/重试/死信…） | — |

- **粗三态**：`ACCEPTED`（处理中）/ `DELIVERED`（送达）/ `FAILED`（死信）。
- **ops 鉴权**：请求头 `Authorization: Bearer <ops_token>`（默认 `ops-secret`）。

---

## 配置

| 来源 | 说明 |
|---|---|
| 环境变量 `ADDR` | 监听地址（默认 `:8080`） |
| 环境变量 `SUPPLIERS_FILE` | 供应商配置 JSON（见 `suppliers.json`） |
| `secrets.json` | 凭证真值（sops 加密配置文件的轻量替身）；`secret_ref` 形如 `env:NAME` 时取环境变量 |

`suppliers.json` 仅存连接与凭证引用（**不含请求模板**，组装逻辑在适配类代码里）：

```json
[{ "type":"crm-contact", "endpoint":"https://crm.example.com/contact",
   "method":"POST", "secret_ref":"env:CRM_TOKEN", "max_attempts":5, "version":1 }]
```

新增供应商 = 实现一个 `SupplierAdapter`（简单的薄层继承基础类）+ 在 `suppliers.json` 登记。

---

## 设计摘要

```
业务系统 ──submit(type,params)──▶ [ 通知网关 ] ──组装+验签──▶ 外部供应商 API
                                   先落库再 ack            （业务方 fire-and-forget）
```

**核心能力流水线**：接收(幂等+校验) → 持久化(DB 当队列) → 投递(适配类组装+验签+发送) → 重试(退避+熔断) → 兜底(死信+重放)，状态事件经 outbox 分发到 MQ；可观测贯穿全程。

**五个关键决策**（详证见 [`docs/方案决策记录.md`](docs/方案决策记录.md)）：

| # | 决策 | 选择 |
|---|------|------|
| 一 | 网关定位 | 适配中心：收敛供应商对接知识到平台一层 |
| 二 | 组装时机 | 接收时校验 + 投递时由适配类组装（v1 不引入模板、不冻结请求） |
| 三 | 任务领取 | DB 表当队列 + 租约领取（reaper 折叠进领取）+ 领取即 attempts+1 |
| 四 | 供应商适配 | 每供应商一个代码适配类 + 公共能力下沉基础类 + 凭证晚绑定(sops) |
| 五 | 状态回查 | 只读拉取 + MQ outbox 分发(不丢事件) + 终态分层保留 |

**可靠性语义**：at-least-once（先落库再 ack + 重试 + 租约兜崩溃），幂等责任下推给外部接口；**只认 HTTP 2xx 为送达**，不解析响应体业务语义。

**明确不做**（边界）：不解析外部 API 业务语义、不做请求转换 DSL/模板引擎、不保证 exactly-once / 有序投递、不替业务方做本地事务原子性。

**评估过但未采纳**：Redis 分布式锁（DB 条件 UPDATE 已原子去重，且与租约同种 TTL 失效、补不齐）、配置驱动通用签名引擎、webhook 逐个推送、直接上 Kafka 做投递队列。

---

## 目录结构

```
cmd/server/         入口与装配（Echo + worker + outbox publisher）
internal/
  api/      Echo HTTP 接入层
  ingest/   接收：幂等 / 校验 / 先落库再 ack / 返回追踪ID
  deliver/  投递 worker：租约领取 / 组装发送 / 失败分类退避 / 熔断 / 死信
  adapter/  SupplierAdapter 接口 + 基础类 + Bearer/HMAC 实现 + 注册表
  store/    SQLite：热表(当队列) / 归档表 / notification_event(outbox)
  mq/       进程内 pub/sub + outbox publisher
  secret/   凭证 Resolver（sops 文件 / env 后端，投递时晚绑定）
  observ/ model/ config/ metrics/ id/
docs/               需求分析 / 方案决策记录 / 架构图
openspec/           变更提案、规格、设计、任务（spec-driven）
scripts/build.sh    正式构建（检查 + 版本注入 + 交叉编译）
scripts/smoke.sh    端到端演示
```

## 技术栈

Go · **Echo**（HTTP 框架）· SQLite（`modernc.org/sqlite`，纯 Go，兼作投递队列）· 进程内 MQ（状态分发替身）· 本地加密配置/环境变量（凭证后端替身）。MVP 取向：重型基础设施以接口 + 轻量实现替身承载，抽象保留以便后续替换（真 MQ / Vault / 数仓）。

## 文档索引

- [`docs/需求分析.md`](docs/需求分析.md) —— 整体需求分析（对齐作业评分项）
- [`docs/方案决策记录.md`](docs/方案决策记录.md) —— 每个决策怎么选出来的（含未采纳路径）
- [`docs/架构图.md`](docs/架构图.md) —— 分层 / 数据 / 执行流程三视图
- [`AI使用说明.md`](docs/AI使用说明.md) —— AI 在本作业中的使用情况
- [`openspec/changes/notification-delivery-gateway/`](openspec/changes/notification-delivery-gateway/) —— 提案 / 规格 / 设计 / 任务
