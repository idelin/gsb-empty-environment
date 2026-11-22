# 认证入口防护组件

为认证入口实现的防护层，覆盖四类能力：

1. **来源 / 账号两维独立限速**：各自维护滑动窗口，互不影响。
2. **同构响应与同构耗时**：账号不存在与账号存在但密码错误，对外返回完全相同的结构与固定耗时，防止账号枚举与计时侧信道。
3. **轮换来源低速填充识别**：单来源、单账号限速无法触发时，通过全局聚合视图识别「摊薄速率 + 轮换来源」的填充行为，另含单账号多来源喷洒检测。
4. **可观测的锁定依据**：每次锁定都落一条 `LockEvent`，携带规则、范围、主体、阈值与实时计数，可直接用于审计与告警。

## 目录结构

| 文件 | 职责 |
| --- | --- |
| `src/types.ts` | 枚举（规则、范围、结论、内部原因）、配置与接口定义 |
| `src/clock.ts` | 时钟抽象：`SystemClock`（生产）与 `ManualClock`（确定性测试） |
| `src/timing.ts` | 恒定耗时包装 `runInConstantTime` 与账号归一化 |
| `src/hasher.ts` | `ScryptHasher`（生产）与 `FastHasher`（测试） |
| `src/store.ts` | 账号存储接口与内存实现 |
| `src/rate-limiter.ts` | 滑动窗口限速器，来源 / 账号各一个实例 |
| `src/lock-registry.ts` | 来源 / 账号 / 全局三类带 TTL 的锁定注册表 |
| `src/failure-streak.ts` | 账号连续失败计数（成功即清零） |
| `src/detector.ts` | 轮换来源低速填充与单账号多来源喷洒检测 |
| `src/guard.ts` | 编排全部检查的 `AuthGuard` 认证入口 |

## 请求处理链路

```
全局锁定 → 来源锁定 → 来源限速 → 账号锁定 → 账号限速
       → 凭证校验（恒定耗时 + 占位哈希）
       → 连续失败锁定 → 低速填充 / 喷洒聚合检测
```

整条链路被 `runInConstantTime` 包裹，无论走哪个分支都消耗相同的墙钟时间基线（`fixedAuthDurationMs`）。

## 防枚举设计

- 账号不存在时，对一个固定占位哈希执行与真实账号同成本的 `verify`，工作量对齐。
- 凭证错误统一返回 `{ outcome: 'rejected', code: 'invalid_credentials', message: '账号或密码错误' }`，不暴露账号是否存在。
- 所有响应（含限速、锁定）都经过恒定耗时包装，响应时间不携带分支信息。
- 不为不存在账号建立账号级锁定 / 连续失败状态，避免制造可区分的状态侧信道。
- 真实原因（`no_such_account` / `bad_credentials`）仅存在于内部 `Decision`，供服务端审计，不随对外响应返回。

## 检测规则

| 规则 `RuleId` | 范围 | 触发条件（默认值见 `DEFAULT_CONFIG`） |
| --- | --- | --- |
| `source_rate_limit` | 来源 | 窗口内尝试次数超过 `sourceMaxAttempts` |
| `account_rate_limit` | 账号 | 窗口内尝试次数超过 `accountMaxAttempts` |
| `account_lockout` | 账号 | 连续失败达到 `accountLockoutThreshold` |
| `rotating_source_low_slow_fill` | 全局 | 检测窗口内唯一来源数 ≥ `fillDistinctSources` 且失败数 ≥ `fillMinFailures` |
| `account_source_spray` | 账号 | 同账号在窗口内被 ≥ `accountSprayDistinctSources` 个不同来源失败尝试 |

## 可观测性

- `listLockEvents()`：全部锁定事件历史，含规则、范围、主体、原因、证据与 `lockedUntil`。
- `activeLocks()`：当前生效锁快照。
- `snapshot()`：配置、生效锁、事件历史，以及低速填充检测的全局 / 单账号聚合指标。

## 运行测试

```bash
npm test
```

测试使用 Node 内置测试运行器与 `ManualClock`，无需真实等待即可确定性验证固定耗时。
测试分两组流量对照：

- `test/traffic-enumeration.test.ts`：枚举 / 轮换来源流量，验证同构性与全局锁定。
- `test/traffic-normal.test.ts`：正常登录流量，验证合法行为不被误伤且表现一致。
