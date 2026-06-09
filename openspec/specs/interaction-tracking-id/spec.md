# interaction-tracking-id Specification

## Purpose

定义交互追踪 ID 能力：受理时返回唯一追踪 ID（复用 notification id）、追踪 ID 在幂等重提下保持稳定，以及查询与推送统一以追踪 ID 关联。

## Requirements

### Requirement: 提交返回唯一交互追踪 ID

系统 SHALL 在受理通知提交时返回一个代表本次交互的唯一追踪 ID。该追踪 ID SHALL 复用系统生成的 notification id（不另造标识），并在该通知的整个生命周期内稳定不变。

#### Scenario: 受理成功返回追踪 ID
- **WHEN** 业务系统成功提交一条通知
- **THEN** 系统 SHALL 在响应中返回该交互的唯一追踪 ID

#### Scenario: 追踪 ID 全局唯一
- **WHEN** 两次提交对应两次不同的逻辑交互
- **THEN** 系统 SHALL 为它们分配不同的追踪 ID

### Requirement: 追踪 ID 在幂等重提下保持稳定

当业务方以相同 `(source_system, idempotency_key)` 重复提交时，系统 SHALL 返回与首次受理相同的追踪 ID，使一次逻辑交互始终对应同一个追踪 ID。

#### Scenario: 重提返回同一追踪 ID
- **WHEN** 业务方用相同 `idempotency_key` 再次提交同一交互
- **THEN** 系统 SHALL 返回首次受理时的同一追踪 ID，而非新建标识

### Requirement: 查询与推送统一以追踪 ID 关联

系统 SHALL 使追踪 ID 成为业务方追踪投递结果的统一句柄：所有面向业务方的状态查询 SHALL 接受该追踪 ID 作为入参；所有推送到 MQ 的状态事件 SHALL 携带该追踪 ID。

#### Scenario: 凭追踪 ID 查询投递结果
- **WHEN** 业务方携带追踪 ID 查询投递状态
- **THEN** 系统 SHALL 返回该交互对应通知的状态

#### Scenario: 推送事件携带追踪 ID
- **WHEN** 一条状态事件被发布到 MQ
- **THEN** 该事件 SHALL 携带对应交互的追踪 ID，使订阅方可与提交时拿到的 ID 关联
