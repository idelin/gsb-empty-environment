# LS/SPF Routing Engine

OSPF 风格的链路状态路由计算引擎（TypeScript，零依赖，Node.js ≥ 23.6 直接运行 `.ts`）。

## 组件

- `src/lsdb.ts` — 链路状态数据库：按序列号接收 LSA，拒绝过期/重复通告。
- `src/spf.ts` — SPF 计算器：
  - `fullSpf`：标准 Dijkstra（二叉堆，确定性 tie-break）。
  - `incrementalSpf`：增量 Dijkstra（Narvaez 式动态 SPT 更新）。链路删除/权值增大时，
    仅失效旧最短路径树中经过恶化边的子树，以未受影响节点为种子做 warm-start Dijkstra；
    链路新增/权值减小由同一过程自然吸收。测试保证其结果与全量重算逐点一致。
  - `buildGraph`：OSPF 双向可达检查（two-way check）后才将链路纳入拓扑。
- `src/node.ts` — 路由器：维护 LSDB/FIB，接口变化时发起 LSA，收到新 LSA 时泛洪并调度 SPF；
  内置路由震荡避免——10 tick 窗口内 LSDB 频繁变化时 SPF hold-down 指数退避（0→1→2→4→8 tick），
  静默窗口后自动复位，全程输出日志。
- `src/network.ts` — 离散 tick 网络仿真：链路切断/恢复/改权值、LSA 泛洪、逐 tick 推进、
  `traceRoute` 转发仿真（检测环路/黑洞）、`checkAllPairs` 全对连通性校验。
- `src/topology.ts` — 8 节点多环拓扑：两个环（N1-N4、N5-N8）+ 主备骨干（N3-N5 主、N4-N8 备）
  + 长途链路 N2-N6。

## 运行

```bash
npm test     # node --test（Node 内置测试运行器）
npm run demo # 完整演示：收敛 -> 断纤 -> 改权值 -> 震荡抑制
```

## 验收场景（demo 输出摘要）

```
=== Phase 1: initial LSA flooding + SPF convergence (8-node multi-ring) ===
[tick   1] N1     SPF full run #1: 7 FIB changes: +N2 via N2 cost 1; ...
Converged at tick 1. All-pairs check: OK (no loops, no blackholes)
  trace N1 -> N5: N1 -> N2 -> N3 -> N5

=== Phase 2: fiber cut on core backbone link N3-N5 ===
[tick   1] NET    *** link N3-N5 CUT (fiber failure) ***
[tick   2] N3     originate LSA #2 links=[N2/1 N4/1]
[tick   2] N1     SPF incremental (recomputed 2/8 nodes) run #2: ~N5 via N4 cost 4 (was via N2 cost 3); ...
Reconverged at tick 2 (took 1 tick). All-pairs check: OK (no loops, no blackholes)
  trace N1 -> N5: N1 -> N4 -> N8 -> N5  (rerouted via backup backbone N4-N8)

=== Phase 3: metric change on backup backbone N4-N8 (cost 2 -> 10) ===
  trace N1 -> N5: N1 -> N2 -> N6 -> N5  (now via long-haul N2-N6)

=== Phase 4: link flapping N2-N6 -> route oscillation avoidance (SPF backoff) ===
[tick   4] N2     route oscillation avoidance: LSDB flapping (4 changed ticks/10), SPF hold-down backoff -> 1 ticks
[tick   5] N2     route oscillation avoidance: LSDB flapping (5 changed ticks/10), SPF hold-down backoff -> 4 ticks
N2 SPF runs during 10-tick flap storm: 3 total (throttled by hold-down backoff)

=== Phase 5: flapping stops, network stabilizes ===
Final state at tick 21. All-pairs check: OK (no loops, no blackholes)
```

无瞬时环路/黑洞的关键：所有节点在同一 tick 内基于完全一致的 LSDB 同步运行 SPF 并原子安装 FIB；
链路代价为正 ⇒ 沿转发路径距离严格递减 ⇒ 不可能成环。测试在每个 tick 边界对全部 8×7
节点对做转发仿真验证。

## 测试结果

```
$ npm test
✔ initial convergence: all 8x7 pairs reachable, loop-free
✔ core backbone cut: converges within a few ticks, no transient loops or blackholes
✔ metric change triggers reconvergence onto the alternate path
✔ link flapping triggers route oscillation avoidance (SPF hold-down backoff)
✔ link restoration reconverges to the original optimum
✔ full Dijkstra computes known shortest distances
✔ two-way check hides half-visible links
✔ incremental SPF after link removal matches full recompute
✔ incremental SPF after cost decrease matches full recompute
✔ incremental SPF across random topologies equals full Dijkstra
✔ LSDB rejects stale and duplicate LSAs
ℹ tests 11  ℹ pass 11  ℹ fail 0
```
