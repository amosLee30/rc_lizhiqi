## ADDED Requirements

### Requirement: 接收通知提交

系统 SHALL 提供一个接收 API，接受业务系统提交的 `{idempotency_key, type, params}`，其中 `type` 映射到一个供应商配置，`params` 为业务入参。

#### Scenario: 合法提交被接受
- **WHEN** 业务系统提交携带合法 `type` 与完整 `params` 的请求
- **THEN** 系统 SHALL 返回一个系统生成的 notification id 与受理结果（ACCEPTED）

#### Scenario: 未知通知类型被拒绝
- **WHEN** 提交的 `type` 在 `supplier_config` 中不存在
- **THEN** 系统 SHALL 同步拒绝该请求并返回明确错误，不落库

### Requirement: 提交幂等去重

系统 SHALL 以 `(source_system, idempotency_key)` 作为幂等键，对重复提交去重。

#### Scenario: 重复提交返回同一记录
- **WHEN** 同一 `source_system` 用相同 `idempotency_key` 再次提交
- **THEN** 系统 SHALL 不创建新的通知记录，并返回已存在记录的 notification id

### Requirement: 接收时参数校验并存储原始入参

系统 SHALL 在接收时调用对应供应商适配类对必须参数做完整性校验（fail-fast），并存储原始 `params`。系统 SHALL NOT 在接收时组装或冻结目标请求——请求组装与验签在投递时由适配类完成。

#### Scenario: 必须参数缺失同步失败
- **WHEN** `params` 缺少该供应商必须的参数
- **THEN** 系统 SHALL 在接收阶段同步返回校验失败，不落库、不进入投递

#### Scenario: 受理仅存原始入参
- **WHEN** 一条通知被成功受理并落库
- **THEN** 系统 SHALL 存储原始 `params`，不存储已组装/含签名的目标请求

### Requirement: 持久化后再确认

系统 SHALL 先将通知持久化、再返回受理成功（先落库再 ack），以此作为 at-least-once 的起点。

#### Scenario: 落库失败不返回成功
- **WHEN** 通知持久化失败
- **THEN** 系统 SHALL NOT 返回受理成功，业务方据此可安全重试
