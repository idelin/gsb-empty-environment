# 隐私预算账本（budget-ledger）

仅用 Python 3.11 标准库实现的持久化 epsilon 预算账本，提供查询预算的
**预留（reserve）/ 提交（commit）/ 取消（cancel）**，并附带一个独立的
账本状态机核对器。**仅处理合成数据；本仓库中的统计实验只是健全性检查，
不构成任何形式化隐私证明。**

## 参数与定义

| 名称 | 含义 |
| --- | --- |
| `total_epsilon` | 账本总预算，初始化时写入 `meta` 表，之后不可变更 |
| `sensitivity` | 查询全局敏感度（默认 1.0），Laplace 噪声尺度为 `sensitivity / epsilon` |
| `epsilon`（每查询） | 单次查询预留/消耗的预算，必须为正有限数 |
| `query_id` | 查询身份；幂等键 |
| `content` | 查询内容，以规范化 JSON 的 SHA-256 作为内容指纹 |
| 组合规则 | **明示 epsilon 求和**（basic composition）：已发布查询的总消耗 = Σ εᵢ，见 `BudgetLedger.composition_bound()` |

状态机：`RESERVED → PUBLISHED` 或 `RESERVED → CANCELLED`，终态不可变更。

- **幂等**：相同 `query_id` + 相同内容的 `reserve`/`commit`/`cancel` 重放安全，
  返回既有记录。
- **冲突**：相同 `query_id` + 不同内容（含并发场景）抛 `ConflictError`。
- **取消**：仅发布前可取消并返还预留；已发布查询取消抛 `InvalidStateError`，
  **预算不退还**。
- **并发**：所有预留检查在 `BEGIN IMMEDIATE` 事务内完成，并发预留总额
  不会超过 `total_epsilon`。

## 持久事务与提交边界故障枚举

`commit` 把「可用性校验 → 噪声生成 → 结果发布 → 预算扣减」放进**同一个
SQLite 事务**（WAL + `synchronous=FULL`）。故障注入点枚举于
`budget_ledger.FAULT_POINTS`：

| 故障点 | 位置 | 重试行为 |
| --- | --- | --- |
| `before_tx` | 事务开始前 | 无副作用，安全重试 |
| `after_availability_check` | 事务内 | 整体回滚，安全重试 |
| `after_noise_generated` | 事务内 | 整体回滚，安全重试 |
| `after_publish_recorded` | 事务内 | 整体回滚，安全重试 |
| `after_budget_deducted` | 事务内 | 整体回滚，安全重试 |
| `after_tx_commit` | 提交后、响应前（崩溃） | 发布已持久化，重试**复用原噪声与预算记录**（`noise_reused=True`） |

## 独立核对器

`ledger_checker.LedgerChecker` 独立于写入路径，重放公开事件日志并交叉
核对查询表，验证：

1. 预算守恒：`total == reserved + spent + available` 且日志支出与表一致；
2. 每个 `query_id` **最多一次发布**（`publish_seq` 唯一）；
3. 状态机迁移合法，不存在「发布后取消」的退款行为；
4. 公开日志卫生：不含 `noise`/`seed`/`secret`/`rng`/`random_state` 等
   密钥或随机状态字段（噪声只存于私有的 `queries` 表）。

## 文件

- `budget_ledger.py` — 账本主体（预留/提交/取消、噪声、持久事务、故障注入）
- `ledger_checker.py` — 独立账本状态机核对器
- `test_budget_ledger.py` — unittest 测试（22 例）
- `demo.py` — 运行样例

## 运行

```sh
python3.11 demo.py
python3.11 -m unittest -v
```

### 样例输出（数值为随机噪声，每次运行不同）

```
-- reserve + commit (atomic: noise, publish, budget deduction)
published result=49.3345 (true=47.0, seq=1)
-- idempotent retry reuses the original noise and budget record
retry result=49.3345 noise_reused=True
-- same ID with different content conflicts
ConflictError: query 'q-count' already exists with different content
-- cancel before publish refunds; after publish it must not
q-sum cancelled, reservation refunded
InvalidStateError: query 'q-count' is already published; budget is not refunded
-- crash after durable publish, then retry on a reopened ledger
simulated crash: after_tx_commit
recovered result=6.7622 noise_reused=True
-- budget snapshot (explicit epsilon-sum composition)
total=1.0 reserved=0 spent=0.6 available=0.4
-- independent ledger checker
events_replayed=6 queries_checked=3 ok=True
-- statistical sanity check (NOT a formal privacy proof)
n=2000 sample_mean=+0.0241 sample_var=7.781 theoretical_var=8.000
```

### 测试结果

```
Ran 22 tests in 0.735s
OK
```

覆盖：幂等预留/提交/取消、同 ID 不同内容冲突（含并发）、预算耗尽、
并发预留不超总额、发布后取消不退款、取消终态、全部 6 个提交边界故障
注入后的安全重试、崩溃后重开账本复用原噪声、组合求和、核对器对
双发/守恒破坏/发布后取消篡改的检测、公开日志无随机状态。

## 免责声明

本仓库仅用于合成数据上的机制演示。Laplace 采样的统计实验（如样本方差
对照）是健全性检查，**不作为形式化隐私证明**；形式化保证需要机制层面的
数学论证，超出本仓库范围。
