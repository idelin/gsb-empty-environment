# Spatio-Temporal Grid Hashing Collision Early-Warning Engine

大规模动态实体近邻接触侦测引擎：三维空间网格 + 时间时隙（Time Slot）分片，
根据实体当前坐标与速度向量外推下一时隙的外包胶囊体（Capsule），
以常数时间哈希查找发现潜在碰撞航线，并在安全距离被突破前输出预警信标。

## 架构

```
src/
  geometry.ts    Vec3 运算、线段-线段最近距离、胶囊体表面间距、弹道接触时间求解
  grid.ts        SpatioTemporalGrid —— (slot, ix, iy, iz) 哈希分桶的时空网格
  engine.ts      CollisionWarningEngine —— 广相裁剪 + 精确胶囊检测 + 预警信标
  simulation.ts  500 个空中交通单元的可复现仿真场景（种子化 PRNG）
dist/            预构建产物（CommonJS + .d.ts），`npm run build` 可用 tsc 重新生成
demo/            运行样例
test/            Node.js 内置断言测试（node:assert；Node >= 18 亦兼容 node:test）
```

### 工作流程（每个评估 tick）

1. **外推**：对每个实体，按速度向量推演未来 `horizonSlots` 个时隙的扫掠胶囊体
   （起点 `p + v·(k-1)Δt`，终点 `p + v·kΔt`，半径为实体半径）。
2. **广相（常数时间）**：胶囊体按 `minSeparation/2` 膨胀后插入时空网格
   （键 = `slot|ix,iy,iz`）。共享同一桶的实体对即为候选碰撞航线——
   哈希插入/查询均为 O(1)，整体 O(n·slots) 而非 O(n²)。
3. **窄相（精确）**：候选对做线段-线段最近距离（Ericson 5.1.9）精确检测；
   确认突破后以相对运动二次方程解出精确突破时刻。
4. **信标**：每个实体对只告警一次，信标携带预测突破时刻与两机当前拓扑距离。

## 运行

```bash
npm test        # Node.js 内置断言测试（无需任何依赖）
npm run demo    # 500 单元仿真运行样例
npm run build   # 有网络环境安装 typescript 后重新编译 src -> dist
```

## 运行样例（`npm run demo`）

```
=== Spatio-Temporal Collision Early-Warning Demo ===
units:            500 air-traffic units
min separation:   50 m
time slot:        1 s
horizon:          10 slots (10 s)

t=0  A(0, 0, 1000) v=(50,0,0)  B(1020, 0, 1000) v=(-50,0,0)
t=0  A<->B topological distance: 1020.0 m

*** WARNING BEACON [t=0s] ***
    pair:                 A <-> B
    current distance:     1020.0 m (surface gap 1000.0 m)
    min safe separation:  50 m
    predicted breach at:  t=9.50s (in 9.50s, slot #10)
    lead time:            9.50s before breach (required >= 5s)

--- ground truth check (1 ms integration of A/B trajectories) ---
actual 50m-separation breach at t=9.500s
engine predicted it at t=9.50s, issued at t=0s -> 9.5s early

--- engine stats (last tick, 500 units) ---
grid cells used:    16565
candidate pairs:    3
precise checks:     3 (brute force would be 124750)
beacons raised:     1
```

## 测试结果（`npm test`，27/27 通过）

```
ok 1 - CollisionWarningEngine > predicts the 50 m breach 9.5 s ahead with current topology distance
ok 2 - CollisionWarningEngine > warning is raised at least 5 s before the breach
ok 3 - CollisionWarningEngine > each pair is alerted only once (dedup across ticks)
ok 4 - CollisionWarningEngine > no alert for traffic keeping safe separation
ok 5 - CollisionWarningEngine > no alert when breach lies beyond the horizon
ok 6 - CollisionWarningEngine > alert for already-breaching pair reports zero time to breach
ok 7 - CollisionWarningEngine > broad phase prunes the vast majority of pairs
ok 8..16 - geometry > 线段距离 / 胶囊间距 / 接触时间求解
ok 17..22 - SpatioTemporalGrid > 时空分桶 / 时隙隔离 / 自排除 / 清除
ok 23 - AirTrafficSimulation acceptance (500 units) > 500 air-traffic units are simulated
ok 24 - AirTrafficSimulation acceptance (500 units) > engine warns about A/B 50 m breach >= 5 s before it happens
ok 25 - AirTrafficSimulation acceptance (500 units) > beacon currentDistance matches the live topology distance
ok 26 - AirTrafficSimulation acceptance (500 units) > prediction matches a fine-grained ground-truth integration
ok 27 - AirTrafficSimulation acceptance (500 units) > engine stays fast and prunes at 500-unit scale

# tests 27
# pass 27
# fail 0
```

## 验收边界对照

| 要求 | 结果 |
| --- | --- |
| 模拟 500 个空中交通单元 | `AirTrafficSimulation` 种子化生成 500 单元（含对抗航线 A/B） |
| 预测未来 10 秒内的接触 | 10 个 1 秒时隙的扫掠胶囊体外推 |
| A 与 B 突破 50 m 最小安全距离 | 预测突破时刻 t=9.50 s，与 1 ms 地面真值积分一致 |
| 碰撞前 5 秒输出预警 | 信标在 t=0 s 发出，提前 9.5 s（≥ 5 s） |
| 输出两机当前拓扑距离 | 信标 `currentDistance = 1020.0 m`（附表面间隙 1000.0 m） |
| 常数时间发现潜在碰撞航线 | 哈希分桶广相：500 单元每 tick 仅 3 次精确检查（暴力需 124750 次） |

## 说明

- 测试仅使用 Node.js 内置模块（`node:assert/strict` 等）；`test/compat.js`
  在 Node >= 18 上直接委托 `node:test`（可 `node --test test/`），
  在低版本 Node 上使用零依赖 shim 执行相同用例。
- 当前环境离线且未安装 `typescript`，`dist/` 为随仓库提交的预构建产物；
  有网环境 `npm install && npm run build` 即可用 tsc 从 `src/` 重新生成。
