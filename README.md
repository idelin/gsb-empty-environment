# csp — 有限整数域约束库（Go）

## 功能
- 变量：有限整数域 `[lo, hi]`（`Problem.AddVar`）
- 约束：
  - `Equality{X, Y, C}`：x = y + C
  - `DiffBound{X, Y, C}`：x - y <= C（界一致传播）
  - `AllDifferent{Vars}`：全异（单例值移除 + 重复单例冲突检测）
- `Propagate()`：定点传播，**保守**——绝不删除任何真实解中的值；
  返回 `false` 表示某域已空（确定无解）；返回 `true` **不代表可解**。
- `Solve(limit)`：完整回溯搜索，是判定无解的唯一权威；返回前完整恢复所有域。
- `Snapshot()` / `Restore()`：域的快照与回滚。

## 验证（csp_test.go）
小域随机问题（500 种子）对照暴力枚举：
- `TestConservativePropagation`：传播后每个暴力解的值仍留在域中
- `TestSolverMatchesBruteForce`：求解器解集与暴力枚举完全一致
- `TestRollbackRestoresDomains`：求解（含无解情形）后域完全恢复
- `TestPigeonholeUnsatOnlyBySearch`：3 变量 {1,2} 全异——传播不报错，回溯确认无解
- `TestPropagateDetectsWipeout`：域清空时传播确定报无解

运行：`GO111MODULE=on go test ./...`
