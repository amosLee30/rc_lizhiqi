## 1. 基础设施与数据模型

- [x] 1.1 搭建服务骨架（Go + Echo 框架：路由/中间件/健康检查，配置加载、日志）
- [x] 1.2 建 `notification` 热表，含 status / attempts / max_attempts / next_attempt_at / lease_owner / lease_until，并加 `(status, next_attempt_at)` 复合索引
- [x] 1.3 建 `notification` 归档表（同结构，仅存终态）
- [x] 1.4 建 `notification_event` 表（状态历史 + outbox，含 coarse_status / published_at）
- [x] 1.5 建 `supplier_config` 配置（endpoint、method、secret_ref、max_attempts、version；不含模板，组装在适配类代码）

## 2. 供应商适配（supplier-adaptation）

- [x] 2.1 定义 `SupplierAdapter` 接口（Validate 必须参数校验 / BuildRequest 请求组装+验签 / HandleResponse 响应处理 / 日志留存），与传输/重试解耦
- [x] 2.2 实现 `BaseSupplierAdapter` 基础类，沉淀公共能力（通用参数校验、日志留存、请求脚手架）
- [x] 2.3 实现 `type`/supplier → 适配类的注册与分派机制
- [x] 2.4 实现 secret resolver 接口 + sops 加密配置文件后端，投递时解析凭证供适配类使用
- [x] 2.5 实现 1~2 个具体供应商适配类（继承基础类，含必须参数校验、请求组装+验签、响应处理、日志留存）

## 3. 接收层（notification-ingestion）

- [x] 3.1 实现接收 API：接受 `{idempotency_key, type, params}`，未知 type 同步拒绝
- [x] 3.2 实现 `(source_system, idempotency_key)` 幂等去重，重复提交返回已有 id
- [x] 3.3 实现提交时调用供应商适配类做必须参数校验（fail-fast），存储原始 params（不冻结请求）
- [x] 3.4 实现先落库再 ack，并在同事务写入 `ACCEPTED` 的 notification_event

## 4. 投递与可靠性内核（reliable-delivery）

- [x] 4.1 实现轮询 + 带条件原子 UPDATE 的租约领取（含过期租约重领），领取即 `attempts+1`
- [x] 4.2 实现批量领取（中等批量）与轮询抖动
- [x] 4.3 实现投递：适配类据 params 组装请求 + resolver 解析凭证 + 验签 → 通用 worker 发 HTTP（硬超时）→ 适配类处理响应 + 日志留存
- [x] 4.4 实现 2xx=送达 判定（不解析响应体业务语义），置 DELIVERED
- [x] 4.5 实现失败分类（可重试 5xx/429/超时/连接重置 vs 不可重试 4xx）与指数退避+抖动重投
- [x] 4.6 实现达到 max_attempts 进 DEAD + 告警
- [x] 4.7 实现单供应商熔断与并发隔离
- [x] 4.8 实现死信重放：用保留的 params + 最新适配类重新组装并重新入队

## 5. 状态回查与可观测（delivery-observability）

- [x] 5.1 实现状态流转时在同事务追加 notification_event
- [x] 5.2 实现运维查询（by id / 幂等键）、死信列表、手动重放接口
- [x] 5.3 实现业务方只读查询：默认粗三态，detail 模式返回执行明细且受访问控制
- [x] 5.4 实现 outbox publisher：先落库后发 MQ、MQ 不可用时恢复后补发、仅发粗三态
- [x] 5.5 实现终态迁出热表入归档表，并保证领取查询只扫非终态
- [x] 5.6 暴露关键指标（受理量、投递成功/失败、重试、死信、各供应商熔断状态）

## 6. 唯一交互追踪 ID（interaction-tracking-id）

- [x] 6.1 接收 API 响应返回追踪 ID（复用 notification id）
- [x] 6.2 幂等重提命中时返回首次受理的同一追踪 ID（与 3.2 去重逻辑对齐）
- [x] 6.3 业务方查询接口以追踪 ID 为入参（与 5.3 对齐）
- [x] 6.4 MQ 状态事件 payload 携带追踪 ID（与 5.4 对齐）

## 7. 验证

- [x] 7.1 单测：失败分类、退避计算、粗三态映射、基础类公共能力、各 SupplierAdapter 的 Validate/BuildRequest+验签（含供应商测试向量）
- [x] 7.2 并发测试：多 worker 领取不重复、过期租约重领
- [x] 7.3 集成测试：幂等去重（含重提返回同一追踪 ID）、at-least-once（崩溃后重投）、不可重试进死信、死信重放、outbox 补发
- [x] 7.4 端到端：提交（拿追踪 ID）→ 投递 → 凭追踪 ID 查询（粗三态/detail）→ MQ 事件订阅（事件携带追踪 ID）
