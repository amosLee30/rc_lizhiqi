## ADDED Requirements

### Requirement: 每供应商一个适配实现类

系统 SHALL 抽象一个供应商适配接口（`SupplierAdapter`），并为每一个供应商提供一个对应的实现类。系统 SHALL 据通知的 `type`/供应商标识选择对应的适配类。v1 MUST NOT 引入模板/脚本引擎做请求组装——请求组装由适配类代码完成。

#### Scenario: 据供应商选择适配类
- **WHEN** 一条通知进入投递且其 `type` 命中某供应商
- **THEN** 系统 SHALL 选用该供应商对应的适配实现类来处理对接

#### Scenario: 不依赖模板引擎
- **WHEN** 实现一个供应商的对接
- **THEN** 其请求组装逻辑 SHALL 由该供应商的适配类代码承载，而非配置模板渲染

### Requirement: 公共能力下沉基础实现类

系统 SHALL 提供一个基础实现类承载各供应商共用的能力（如通用参数校验、日志留存、请求脚手架），各供应商适配类 SHALL 复用基础类、仅实现自身差异部分。

#### Scenario: 简单供应商薄层接入
- **WHEN** 某供应商对接逻辑与公共能力高度一致
- **THEN** 其适配类 SHALL 仅需薄层继承/复用基础类、实现极少量差异代码

### Requirement: 必须参数校验（接收时 fail-fast）

适配类 SHALL 提供对该供应商必须参数的校验能力，系统 SHALL 在接收时调用它做 fail-fast 校验。

#### Scenario: 必须参数缺失同步失败
- **WHEN** 提交的 `params` 缺少该供应商必须的参数
- **THEN** 系统 SHALL 在接收阶段同步返回校验失败，不落库、不进入投递

### Requirement: 请求组装与验签（投递时由适配类完成）

适配类 SHALL 在投递时据 `params` 组装目标请求，并完成该供应商所需的鉴权/验签组合（含时间戳/nonce/签名等现算逻辑）。各供应商异构的验签规范 SHALL 由其适配类代码实现。

#### Scenario: 投递时组装并验签
- **WHEN** worker 投递一条通知
- **THEN** 对应适配类 SHALL 据 `params` 组装请求并现算注入鉴权/签名后交由 worker 发送

### Requirement: 凭证以引用存储并晚绑定解析

系统 SHALL 在配置中仅存储凭证引用（`secret_ref`），MUST NOT 存储凭证真值；凭证真值 SHALL 在投递时由 resolver 解析（晚绑定，v1 后端用 sops 加密配置文件），供适配类组装/验签使用。

#### Scenario: 投递时解析凭证
- **WHEN** 适配类需要凭证组装请求/签名
- **THEN** 系统 SHALL 通过 resolver 据 `secret_ref` 解析出凭证真值后提供给适配类

#### Scenario: 轮换对在途通知生效
- **WHEN** 某供应商凭证在密钥后端被轮换
- **THEN** 尚未投递成功的在途通知 SHALL 在下次投递时解析到新凭证

### Requirement: 响应处理与日志留存

适配类 SHALL 提供响应处理能力，并对请求与响应做日志留存以支撑复盘与排障。

#### Scenario: 留存实际请求与响应
- **WHEN** 一次投递完成（无论成败）
- **THEN** 系统 SHALL 留存该次实际发出的请求与外部响应日志

### Requirement: 适配类与传输职责分离

适配类 SHALL 只负责该供应商的对接（参数校验、请求组装+验签、响应处理、日志）；HTTP 发送、重试、租约与 2xx 送达判定 SHALL 由通用 worker 承担，MUST NOT 由各适配类各自重复实现。

#### Scenario: 适配类不承担传输与重试
- **WHEN** 一个适配类被调用组装请求
- **THEN** 实际的 HTTP 发送与可靠性处理 SHALL 由通用 worker 完成
