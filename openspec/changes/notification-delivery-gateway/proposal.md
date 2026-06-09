## Why

企业内部多个业务系统在关键事件（注册引流、订阅付款、下单等）发生时，需要调用外部供应商的 HTTPS API 进行通知。各供应商请求地址、Header/Body 格式、鉴权与签名方式各不相同，而业务系统不关心返回值、只需确保通知被稳定可靠地送达。当前没有统一承载这一能力的组件，导致每个业务系统各自重复实现重试与对接逻辑，且业务事务与不可靠的外部网络调用耦合在同一故障域。

本变更引入一个**出站通知可靠投递网关**（适配中心模式），把"可靠投递"与"供应商对接知识"收敛到平台一层，让业务方 fire-and-forget。

## What Changes

- 新增**接收 API**：业务方提交 `{idempotency_key, type, params}`，按来源系统幂等去重，提交时调用供应商适配类做必须参数校验（fail-fast）、存储原始入参，先落库再返回 ack，并返回代表本次交互的唯一追踪 ID。
- 新增**唯一交互追踪 ID**：提交即返回稳定唯一 ID（复用 notification id），幂等重提返回同一 ID，后续查询与 MQ 推送统一携带该 ID。
- 新增**可靠投递内核**：DB 表当队列 + 轮询 + 租约领取（reaper 折叠进领取查询），at-least-once 语义；指数退避重试、失败分类、单供应商熔断/并发隔离、死信兜底与重放。
- 新增**供应商适配**：抽象 `SupplierAdapter` 接口、每供应商一个实现类（公共能力下沉基础实现类），负责必须参数校验、请求组装 + 验签、响应处理与日志留存；凭证以 `secret_ref` 存储、投递时晚绑定解析。v1 先不引入模板引擎，请求组装由适配类代码承载。
- 新增**状态回查与可观测**：`notification_event` 状态历史表兼作 MQ outbox；只读查询（粗三态默认、detail 模式限运维）+ MQ 事件订阅双通道；终态迁出热表入归档表、长期由离线数仓承接。
- **明确不做**：不解析外部 API 业务语义（只认 2xx）、不做请求转换 DSL、不保证 exactly-once / 有序投递、不替业务方做本地事务原子性。

## Capabilities

### New Capabilities
- `notification-ingestion`: 接收业务方提交、幂等去重、提交时渲染冻结业务骨架与参数校验、先落库再 ack。
- `reliable-delivery`: 任务领取（租约+reaper）、投递发起、at-least-once 语义、退避重试、失败分类、熔断隔离、死信兜底与重放。
- `supplier-adaptation`: 每供应商一个适配实现类（接口 + 基础类），含必须参数校验、请求组装 + 验签、响应处理与日志留存；凭证 secret_ref 晚绑定解析；公共能力下沉基础类；v1 不引入模板引擎。
- `delivery-observability`: 状态查询（粗三态/detail）、notification_event outbox 与 MQ 事件分发、终态分层保留。
- `interaction-tracking-id`: 提交时返回代表本次交互的唯一追踪 ID（复用 notification id），幂等重提返回同一 ID，查询与推送统一以该 ID 关联，便于业务追踪。

### Modified Capabilities
<!-- 无：openspec/specs/ 当前为空，全部为新增能力。 -->

## Impact

- **新增服务**：通知网关服务（接收层 + 投递 worker + 状态/可观测）。
- **数据存储**：关系型数据库新增 `notification`（含热表/归档表）、`notification_event`、`supplier_config` 表。
- **依赖**：关系型数据库（必需，兼作投递队列）；MQ（仅状态事件分发，pub/sub）；sops（凭证加密）；可选离线数仓（Hive）做长期历史。
- **对接方**：业务系统（提交方、可选订阅状态事件）、外部供应商 API（投递目标）、运维平台（查询/重放/告警）。
- **代码扩展点**：每接入一个供应商 = 新增一个 `SupplierAdapter` 实现类 + 发版（简单供应商只需薄层继承基础类）；放弃纯配置零代码接入，换 v1 的简单直接。
