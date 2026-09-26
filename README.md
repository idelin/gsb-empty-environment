# dpledger — 差分隐私预算账本

预算账本，维护查询预算的**预留（reserve）/ 提交发布（commit）/ 取消（cancel）**。
仅使用 Python 3.11 标准库；测试基于 `unittest`。**只处理合成数据。**

## 参数与定义

- `total_epsilon`：账本总预算（纯 DP 的 epsilon 上限）。
- 查询内容 `content`：规范化 JSON（键排序）后取 SHA-256 作为内容身份；
  必须声明 `sensitivity`（L1 敏感度）。
- `epsilon`：单次查询预留/消耗的隐私参数，必须为正。
- 噪声机制：发布值 = 真实值 + Laplace(0, sensitivity/epsilon)（见 `dpledger/noise.py`）。
- **组合规则：明示 epsilon 求和** —— 已发布查询的 epsilon 之和即总消耗上界，
  `spent = sum(epsilon_published)`，任意时刻 `reserved + spent <= total`。

## 语义

- **幂等**：同一 `query_id` + 相同内容重复预留/提交/取消返回原结果；
  重复提交复用原噪声值与预算记录，最多发布一次、只扣一次预算。
- **冲突**：同一 `query_id` 携带不同内容（哈希不同）即 `ConflictError`，
  包括并发场景。
- **取消**：仅发布前可取消并返还预算；已发布查询取消报 `InvalidStateError`，
  不退还预算。
- **持久事务**：预留、噪声生成、结果发布与预算扣减在同一 SQLite 事务内完成。
  提交边界故障枚举（`fault_hooks` 注入点，见 `dpledger/ledger.py`）：
  1. `commit:before_txn` —— 事务前崩溃：无持久化；
  2. `commit:before_db_commit` —— 落库前崩溃：整体回滚；
  3. `commit:after_db_commit` —— 落库后崩溃：重试幂等复用原噪声与预算记录。
- **并发**：`BEGIN IMMEDIATE` + 进程内锁序列化事务；并发预留不会超出总额。
- **独立校验**：`dpledger/checker.py` 的状态机只消费公开事件日志，
  核对预算守恒与最多一次发布；公开日志不含种子/RNG 状态/真实值等秘密字段
  （白名单 + 黑名单双重检查）。

## 运行

```bash
python3 demo.py                              # 运行样例（合成数据）
python3 -m unittest discover -s tests -v     # 测试
```

## 免责声明

本仓库中的统计实验与样例输出仅用于演示账本机制，**不作为形式化隐私证明**。
所有数据均为程序生成的合成数据。
