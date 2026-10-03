# Spacetime Collision Sentinel

大规模动态实体近邻接触侦测引擎：三维空间网格 + 时隙（Time Slot）分片的时空哈希，
根据实体当前坐标与速度向量推演下一时隙的外包胶囊体（Capsule），常数时间发现潜在碰撞航线，
并在预测突破最小安全距离前精准输出预警信标。

## 架构

- `src/vec3.ts` — 三维向量运算与匀速外推。
- `src/capsule.ts` — 胶囊体（扫掠线段 + 半径）；线段-线段最近距离、胶囊表面间距，
  以及匀速运动下突破安全间距的解析解（二次方程求根）。
- `src/spacetimeGrid.ts` — 时空哈希网格。空间按 `cellSize` 立方体划分，时间按
  `slotDuration` 时隙分片；胶囊按 AABB 覆盖的所有格子散列，插入与近邻查询均为均摊 O(1)。
- `src/engine.ts` — 预警引擎。每次 `scan()` 将每个实体在预测时域（horizon）内逐时隙
  外推胶囊体做宽相位配对，再对候选对做窄相位精确检测（胶囊间距 + 解析突破时刻），
  当突破时刻进入 `warningLeadTime` 窗口时发射预警信标（含两机当前拓扑距离）。
- `src/demo.ts` — 500 个空中交通单元的运行样例。

## 运行

需要 Node.js >= 22.6（原生 TypeScript type-stripping 与内置 `node:test`）。

```sh
npm test    # Node.js 内置测试运行器
npm run demo
```

## 验收场景

实体 A、B 以 200 m/s 相对速度对头飞行，引擎配置：预测时域 10 s、最小安全距离 50 m、
预警提前量 5 s。样例输出：

```
Spacetime collision sentinel: 500 entities, horizon=10s, minSeparation=50m, lead=5s
[BEACON] t=5.0s  A <-> B  breach in 5.00s (at t=10.00s)  current distance 1050.0m  (min safe 50m)

Active warnings at t=15s: 0
Avg scan time over 16 scans with 500 entities: 9.99 ms
```

引擎在 t=0 即预测到 t=10 的突破（10 s 时域内），并在碰撞前恰好 5 秒（t=5）
输出预警信标与两机当前距离 1050 m。
