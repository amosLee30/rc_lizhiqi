## ADDED Requirements

### Requirement: 状态变更记录为事件历史

系统 SHALL 在每次通知状态变更时，于同一数据库事务内写入 `notification_event`（含起止状态、粗三态、尝试序号、响应码、错误、发生时间）。该表 SHALL 同时充当状态历史与 MQ outbox。

#### Scenario: 状态变更落历史
- **WHEN** 一条通知的状态发生流转
- **THEN** 系统 SHALL 在同一事务内追加一条对应的 `notification_event` 记录

### Requirement: 运维侧查询、重放与告警

系统 SHALL 为运维/平台提供按 id 或幂等键查询、列出死信、手动重放的能力，并在通知进入死信时告警。

#### Scenario: 运维查询单条投递进度
- **WHEN** 运维按 notification id 或 `(source_system, idempotency_key)` 查询
- **THEN** 系统 SHALL 返回该通知当前状态与执行过程信息

#### Scenario: 运维重放死信
- **WHEN** 运维对一条 DEAD 通知发起重放
- **THEN** 系统 SHALL 使该通知重新进入投递流程

### Requirement: 业务方只读状态查询

系统 SHALL 为业务方提供只读状态查询。默认 SHALL 仅返回粗三态（`ACCEPTED` / `DELIVERED` / `FAILED`）；detail 模式返回状态历史、重试次数与报错，且 SHALL 受访问控制限制（仅运维/特权方）。

#### Scenario: 默认返回粗三态
- **WHEN** 业务方查询某通知状态且未启用 detail
- **THEN** 系统 SHALL 仅返回粗三态，内部状态（PENDING/DELIVERING/RETRYING/DEAD）映射为粗三态

#### Scenario: detail 模式受控
- **WHEN** 无特权方请求 detail 模式
- **THEN** 系统 SHALL 拒绝返回内部执行明细

### Requirement: 经 outbox 分发状态事件

系统 SHALL 通过 outbox 将粗三态状态事件可靠地发布到 MQ 供业务方按需订阅：先落库后发布，MQ 不可用时由 publisher 在恢复后补发，MUST NOT 丢失事件。MQ SHALL 仅用于状态事件分发，MUST NOT 承担投递工作队列职责。

#### Scenario: 事件不丢
- **WHEN** MQ 临时不可用
- **THEN** 状态事件 SHALL 保留在 outbox 中，并在 MQ 恢复后被补发

#### Scenario: 仅发布粗三态
- **WHEN** 一条状态事件被发布到 MQ
- **THEN** 该事件 SHALL 仅含粗三态，不含内部执行明细

### Requirement: 终态分层保留

系统 SHALL 将终态通知（DELIVERED/DEAD）迁出热表存入归档表，使投递领取查询仅扫描非终态记录。归档表 v1 SHALL 暂不清理，长期历史由离线数仓承接。

#### Scenario: 终态迁出热表
- **WHEN** 一条通知到达终态
- **THEN** 该记录 SHALL 被迁出热表，领取查询 SHALL NOT 再扫描到它

#### Scenario: 归档仍可查询
- **WHEN** 对账或排障查询一条已归档的终态通知
- **THEN** 系统 SHALL 能从归档表返回其结果
