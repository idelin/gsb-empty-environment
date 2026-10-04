# dishex — 离散六边形分层空间索引 + 反向补偿事务编排 (Go, 仅标准库)

两个独立组件：

- `hexgrid/`：基于二十面体展开的离散六边形地理网格。经纬度 → 分层 64 位
  网格编码（十六进制字符串）、父子单元缩放、纯整数拓扑 K-Ring 邻域遍历。
- `saga/`：长生命周期流程的失败逆向补偿编排器，保证最终一致性与操作幂等。

## 设计要点

### hexgrid

- 二十面体 12 顶点 / 20 面（边长 2，含等边性与外法线方向测试）。
- 编码：经纬度 → 单位球面 → 最近面（法线点积最大）→ gnomonic 投影到面
  平面 `{x : x·n = h}` → 面内正交基 → pointy-top axial 坐标 → cube 取整。
  这是唯一使用浮点三角函数的路径。
- 64 位索引布局：`mode(4) | res(4) | face(5) | i+bias(17) | j+bias(17) | reserved(17)`，
  分辨率 0–15，十六进制字符串为定长 16 字符。
- `Parent`/`Children`：纯整数 cube 减半取整（aperture-4 层次），无浮点。
- `Neighbors`/`KRing`/`GridDistance`：axial/cube 坐标纯整数运算，
  **无浮点、无三角计算**。`KRing(k)` 返回 `1+3k(k+1)` 个单元，中心在前，
  之后按环顺时针展开。

### saga

- 状态机：`Pending → Running → Completed`
  或 `Running → Compensating → Compensated / Failed`。
- 幂等：`Run` 可并发/重复调用，步骤**恰好执行一次**，所有调用者观察到
  相同的终态与结果（`sync.Cond` 等待终态广播）。
- 失败时任一步骤触发已执行前缀的**严格逆序补偿**；补偿使用
  `context.Background()`，调用方取消不会中断回滚。
- 补偿按 `MaxCompensateRetries` 重试（可配 `Backoff`）；耗尽重试进入
  `Failed`，`Uncompensated()` 精确报告未能回滚的步骤（泄漏清单）。
- 全程记录 `Transitions`（状态迁移日志）与 `Records`（每步骤执行流水）。

## 运行样例

```
$ go run ./cmd/demo
GPS (39.9042, 116.4074) @ res=8 -> cell 1813ffb1ff900000
  face=2 axial=(-20,-56)
  cell centre: (39.9006, 116.4638)
  parent res=4: 1413fffdfff80000  parent res=0: 10140001fffe0000
k=3 ring disk: 37 cells (formula 1+3k(k+1)=37)
  closure: holes=0 disconnected-boundary=0 -> CLOSED, NO GAPS

saga: reserve -> authorize -> charge(fails) -> notify
  final status: Compensated
  result: saga rolled back after step failure: card declined
  event order: [act:reserve act:authorize act:charge cmp:authorize cmp:reserve]
  transition: Pending -> Running (start)
  transition: Running -> Compensating (step "charge" failed: card declined)
  transition: Compensating -> Compensated (reverse compensation completed)
```

## 测试结果

`go test -race ./...`（Go 1.11，darwin/amd64），23 个测试全部通过：

```
ok  dishex/hexgrid   1.807s   (11 tests)
ok  dishex/saga      1.925s   (12 tests)
```

hexgrid 覆盖：编码确定性与十六进制格式、解析往返、二十面体几何自洽、
中心点往返重编码、父子层次一致性（逐级=跳跃、Children 包含关系）、
K-Ring 计数/唯一性/环半径、**半径 3 环闭合无空洞**（内部单元 6 邻居全在
盘内、边界单元全部连通）、邻居对称性、跨分辨率不可比较、非法分辨率 panic。

saga 覆盖：成功路径（资源按设计提交）、失败严格逆序补偿、首步失败零补偿、
空 saga、nil action、补偿重试至成功、补偿耗尽进入 Failed 且精确报告 1 个
未回滚资源、32 并发 `Run` 恰好一次语义、终态后重复 `Run` 幂等、调用方
取消后补偿仍完整执行、200 个并发 saga 资源计数归零且 goroutine 无增长。
