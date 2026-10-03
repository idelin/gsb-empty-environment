# kmatcher — 带权二分图最大权匹配与增广路径器

基于 Kuhn-Munkres（KM/匈牙利）算法（原始-对偶 + 交错增广轨），仅使用 Go 标准库。

## 功能

- 工人 × 任务完全二分图，边权表示产值，`Solve` 输出全局总产值最大的匹配方案。
- `SetWeight(w, t, v)`：动态调整单条边权重（下一次求解自动重置顶标并重算）。
- `MaskWorker(i, true)` / `MaskTask(j, true)`：屏蔽节点（如下线/请假）。屏蔽只拆除受影响的
  匹配对，保留可行顶标，仅对空出的节点重新寻找交错增广轨，实现局部调整、快速切换次优匹配。
- `ResetLabels()`：按当前权重重置顶点顶标（`lx[i] = max 出边权, ly[j] = 0`）并清空匹配。
- `Labels()`：读取当前顶标（对偶势），可用于检验可行性 `lx[i]+ly[j] >= w[i][j]`。
- 求解结果包含完全匹配方案、总产值、未匹配节点及其机会成本（其最优可用边的权值），
  以及未匹配最小机会成本 `MinOpportunityCost`。

## 结构

- `matcher.go`：KM 匹配器（内部补零方阵，屏蔽边记为 forbidden）。
- `matcher_test.go`：单元测试 + 与暴力枚举对拍的随机模糊测试。
- `cmd/demo/main.go`：高产值工人请假下线 → 快速切换次优匹配的完整演示。

## 运行

```sh
go test ./...        # 运行测试（Go <= 1.11 需 GO111MODULE=on）
go run ./cmd/demo    # 运行演示
```

## 演示输出

```
== 初始最大权匹配 ==
  工人0 -> 任务0  产值 100
  工人1 -> 任务3  产值 35
  工人2 -> 任务2  产值 30
  工人3 -> 任务1  产值 45
  总产值: 210

>> 工人0 (最高产值) 临时请假下线，局部调整顶标并重新增广
== 切换后的次优匹配 ==
  工人1 -> 任务0  产值 90
  工人2 -> 任务2  产值 30
  工人3 -> 任务1  产值 45
  总产值: 165
  未匹配 任务3  机会成本 35
  未匹配最小机会成本: 35

>> 任务2 的产值临时上调: 工人3-任务2 15 -> 95
== 权重调整后的匹配 ==
  工人1 -> 任务0  产值 90
  工人2 -> 任务1  产值 50
  工人3 -> 任务2  产值 95
  总产值: 235
  未匹配 任务3  机会成本 35
  未匹配最小机会成本: 35

>> 工人0 返岗
== 恢复后的匹配 ==
  工人0 -> 任务0  产值 100
  工人1 -> 任务3  产值 35
  工人2 -> 任务1  产值 50
  工人3 -> 任务2  产值 95
  总产值: 280
```

## 测试结果

```
=== RUN   TestClassic3x3
--- PASS: TestClassic3x3 (0.00s)
=== RUN   TestRectangularMoreTasks
--- PASS: TestRectangularMoreTasks (0.00s)
=== RUN   TestRectangularMoreWorkers
--- PASS: TestRectangularMoreWorkers (0.00s)
=== RUN   TestMaskWorkerFastSwitch
--- PASS: TestMaskWorkerFastSwitch (0.00s)
=== RUN   TestMaskTask
--- PASS: TestMaskTask (0.00s)
=== RUN   TestSetWeightDynamic
--- PASS: TestSetWeightDynamic (0.00s)
=== RUN   TestOpportunityCost
--- PASS: TestOpportunityCost (0.00s)
=== RUN   TestLabelsFeasibleAfterSolve
--- PASS: TestLabelsFeasibleAfterSolve (0.00s)
=== RUN   TestFuzzAgainstBruteForce
--- PASS: TestFuzzAgainstBruteForce (0.00s)
PASS
ok  	kmatcher	0.320s
```
