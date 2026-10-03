# hexsaga — 离散六边形分层空间索引 + 反向补偿事务编排 (Go)

纯 Go 标准库实现，无第三方依赖。

## 组成

### `grid` — 基于二十面体展开的离散六边形地理网格 (DGGS)

- **编码管线**：经纬度 → 单位球面向量 → 最近二十面体面（平分面半空间测试）
  → 中心投影到面平面 → 六边形晶格量化 → （基座单元， 7 进制位）分解 → 64 位索引。
- **分层结构**：孔径 7 (aperture-7) 层次，整数线性映射 `M = [[2,-1],[1,3]]`
  （det = 7，即六边形空间中 ×√7 带旋转）。任意轴向坐标唯一分解为
  `c = M·parent + offset[d]`，因此 res-r 单元 = res-0 基座单元 + r 个 7 进制位；
  由于 `Z² = M^r·Z² + G_r`（Gosper 余集分解），每层单元恰好铺满整个面平面一次。
- **跨面一致性**：六边形晶格直接位于二十面体面平面上，基向量沿面棱对齐，
  所有面框架统一为右手系，因此相邻面的晶格在展开网中沿共享棱完全重合，
  跨面量化无混叠。
- **K-Ring 拓扑遍历**：纯整数轴向/立方坐标 BFS 扩展，每个单元从自身晶格
  发出 6 个邻居坐标，再锚定回球面（仅向量点积/归一化，**无浮点三角函数**）
  并重编码。环覆盖按构造闭合无空洞。
- **索引位布局**（64 位，输出 16 位十六进制字符串）：
  `[4 mode][4 res][5 face][5 baseQ][5 baseR][13×3 digits][2 reserved]`，
  最大分辨率 13。

### `saga` — 长生命周期流程的失败逆向补偿编排

- **状态机**：`PENDING → RUNNING → COMPLETED / COMPENSATING → COMPENSATED / FAILED`。
- **逆向补偿**：任一步骤失败（含 panic、context 取消）时，已完成步骤严格
  按 LIFO 逆序补偿；补偿本身失败则进入终态 `FAILED`。
- **幂等**：每个 saga ID 的正向执行与补偿都恰好一次；并发/重复 `Run` 调用
  序列化到同一状态机，所有调用方观察到相同的终态结果。
- **无泄漏**：编排器不启动任何后台 goroutine；`AssertConsistent()` 断言
  所有 saga/步骤均处于终态。

## 运行

```bash
go test ./...        # 全部测试（含 -race 可通过）
go run ./cmd/demo    # 运行样例
```

## 样例输出

```
[grid] encode(39.9042, 116.4074, res=8) = 180464826caafffe
[grid] cell center = (39.90255, 116.40560), face = 0
[grid] parent@res5 = 150464826dfffffe, children of parent = 7
[grid] k-ring(k=3) around 180464826caafffe: 37 cells (1 + 6 + 12 + 18 = 37)
[grid] closure check: ring(3) cells missing from expansion of ring(2): 0 (0 = closed, no holes)

[saga] execute  reserve-inventory
[saga] execute  charge-payment
[saga] execute  dispatch-courier -> FAILED (courier unavailable)
[saga] compensate charge-payment (refund)
[saga] compensate reserve-inventory (release stock)
[saga] saga order-1001 finished: status=COMPENSATED err=...
[saga] idempotent replay: status=COMPENSATED err=... (no step re-executed)
[saga] final consistency check: no leaked/running steps
```

## 测试覆盖

- `grid`：编码格式/确定性/参数校验、父子层级（digit 前缀截断）、
  折叠-展开双射（全平面铺砌）、中心回编幂等、K-Ring 数量公式
  `1+3k(k+1)`、环闭合无空洞（扩展等价性）、全球 198 点 + 两极的
  3 环覆盖、邻接对称性、环壳基数恒等式。
- `saga`：成功路径顺序、各位置失败的逆序补偿（首步/中间/末步）、
  nil 补偿跳过、步骤 panic 恢复、补偿 panic 进入 FAILED、成功/补偿后
  幂等重放、同 ID 并发 exactly-once（64 goroutine）、128 saga 混合并发
  无泄漏（goroutine 计数断言）、context 取消补偿、预取消、
  泄漏检测器自检、非法输入、快照隔离。
