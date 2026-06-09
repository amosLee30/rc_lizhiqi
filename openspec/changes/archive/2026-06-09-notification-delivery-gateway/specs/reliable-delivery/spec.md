## ADDED Requirements

### Requirement: 投递语义为至少一次

系统 SHALL 提供至少一次（at-least-once）投递语义，并将去重幂等责任下推给外部接口（或在投递中携带幂等标识）。系统 SHALL NOT 声称提供 exactly-once。

#### Scenario: 受理后保证最终投递或进入死信
- **WHEN** 一条通知被成功受理
- **THEN** 系统 SHALL 反复尝试直至投递成功，或在耗尽重试后进入死信，不静默丢弃

### Requirement: 基于租约的任务领取

worker SHALL 通过带条件的原子 UPDATE 抢占租约来领取待投递任务，保证同一条任务在同一时刻只被一个 worker 领取。领取查询 SHALL 仅扫描可领取记录（`next_attempt_at <= now()` 且未被有效租约占用）。

#### Scenario: 并发领取不重复
- **WHEN** 多个 worker 同时尝试领取同一条 PENDING 任务
- **THEN** 仅有一个 worker 领取成功，其余领取该条失败

#### Scenario: 过期租约可被重新领取
- **WHEN** 某条任务处于 DELIVERING 但其租约已过期（worker 崩溃或卡死）
- **THEN** 后续领取查询 SHALL 将该任务视为可领取并重新领取，无需独立 reaper 进程

### Requirement: 尝试次数在领取时计数

系统 SHALL 在领取（转入 DELIVERING）时即对 `attempts` 加一，使反复使 worker 崩溃的"毒丸"任务也能累加到上限并进入死信。

#### Scenario: 毒丸任务最终进入死信
- **WHEN** 某条任务每次被领取都导致处理中断且不产生明确结果
- **THEN** `attempts` SHALL 持续累加，达到 `max_attempts` 后该任务进入 DEAD

### Requirement: 以 HTTP 2xx 判定送达

系统 SHALL 仅以外部 API 的 HTTP 传输层结果判定送达：2xx 视为送达成功，MUST NOT 解析响应体业务语义。

#### Scenario: 2xx 标记送达
- **WHEN** 外部 API 返回 2xx
- **THEN** 通知 SHALL 被标记为 DELIVERED，不再重试

#### Scenario: 业务级失败不影响送达判定
- **WHEN** 外部 API 返回 2xx 但响应体表示业务失败
- **THEN** 通知 SHALL 仍被标记为 DELIVERED

### Requirement: 失败分类与退避重试

系统 SHALL 区分可重试与不可重试失败：可重试失败（超时、连接重置、5xx、429）SHALL 以指数退避加抖动重试；不可重试失败（如 400/401/403）SHALL 不重试并进入死信。

#### Scenario: 可重试失败按退避重投
- **WHEN** 投递遇到超时或 5xx 且 `attempts < max_attempts`
- **THEN** 系统 SHALL 置一个未来的 `next_attempt_at` 并稍后重投

#### Scenario: 不可重试失败直接进死信
- **WHEN** 投递返回 400/401/403
- **THEN** 系统 SHALL 不再重试并将通知置为 DEAD 且触发告警

### Requirement: 供应商级熔断与隔离

系统 SHALL 对单个供应商提供熔断与并发隔离，使某个长期不可用的供应商不会耗尽全部投递资源、饿死对健康供应商的投递。

#### Scenario: 故障供应商被隔离
- **WHEN** 某供应商持续失败触发熔断
- **THEN** 系统 SHALL 暂缓对该供应商的投递尝试，同时不影响对其他供应商的正常投递

### Requirement: 死信兜底与重放

系统 SHALL 将达到重试上限的通知置入死信状态（DEAD），并支持人工或改配置后重新渲染再重放。

#### Scenario: 死信触发告警
- **WHEN** 一条通知进入 DEAD
- **THEN** 系统 SHALL 触发告警以便人工介入

#### Scenario: 修正对接后重放
- **WHEN** 供应商修正了对接方式（适配类更新或配置修正）且运维对死信通知发起重放
- **THEN** 系统 SHALL 使用保留的原始 `params` 与最新适配类重新组装并重新入队投递
