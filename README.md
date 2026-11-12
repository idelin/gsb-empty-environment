# 隐私访问模式填充

加密存储隐藏内容却暴露每次读取块数。写供Go存储宿主调用的填充规划包，输入对象长度分布、允许桶边界和最大额外字节预算，为每个对象分配不小于真实长度的桶，最小化可观察桶数，再最小化总填充；最多二十对象。返回映射与预算证据，用全分配枚举核对，失败不修改实际对象。

## `padplan` 子包

入口：`padplan.PlanBuckets(padplan.Input, ...Option) (*padplan.Plan, error)`

- 优化目标为字典序：先最小化不同桶大小数量（可观察块数），再最小化总填充字节。
- 规划器枚举桶边界子集（每对象在子集内取最小可行桶），核对器在全部
  `k^n` 个分配上做带剪枝 DFS，独立复算最优值并与规划器比对，不一致即返回
  `ErrVerificationMismatch`。
- `Plan.Evidence` 给出预算、总填充、预算余量、枚举空间（`big.Int`）、
  访问节点数、可行叶子数与节点上限等审计证据。
- 纯函数语义：不修改输入、不触碰存储对象；任何失败路径返回 `nil` 方案与
  哨兵错误（`ErrBudgetExceeded`、`ErrNoFeasibleBucket`、`ErrEnumerationLimit` 等）。
- 枚举默认有 200 万节点上限，可用 `WithEnumerationNodeCap` 调整（`<=0` 关闭）。

验证：`go test ./...`（含 100 组随机实例的暴力枚举一致性属性测试）。
