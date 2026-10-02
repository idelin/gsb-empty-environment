# swim-deterministic-sim

离散事件网络模拟器 + SWIM 协议全链路确定性测试驱动（TypeScript / Node.js 内置测试，零第三方依赖）。

## 确定性保证

系统中只有三个"非确定性来源"，全部被显式接管：

| 来源 | 接管方式 |
| --- | --- |
| 随机性（丢包、抖动、探测目标选择） | 唯一种子 PRNG（mulberry32，`src/rng.ts`），同种子必现同序列 |
| 时间 | 虚拟时钟 + 最小堆事件队列（`src/simulator.ts`），同 tick 事件按插入序 FIFO |
| 冲突仲裁 | 全序仲裁函数 `arbitrate`（`src/swim.ts`）：incarnation 高者优先 → 同 incarnation 状态劣者优先（dead > suspect > alive）→ 再平手按 declaredBy 字典序 |

因此任意一次运行都可以凭种子 100% 复现，竞态 BUG 可精确重放。

## 结构

- `src/rng.ts` — 种子随机数（mulberry32）
- `src/simulator.ts` — 离散事件模拟器：虚拟时钟步进、事件最小堆、确定性事件日志
- `src/network.ts` — 虚拟交换机：按链路配置丢包率 / 基础延迟 / 抖动，支持断链（`cut`）与恢复（`heal`）
- `src/swim.ts` — SWIM 状态机：ping / ack / ping-req 间接探测、suspect→dead 怀疑定时器、incarnation 自增反驳（refute）、周期反熵 gossip、确定性仲裁
- `test/swim.test.ts` — `node:test` 测试套件
- `examples/run-sample.ts` — 可运行的竞态复现样例

## 运行

需要 Node.js ≥ 22.6（使用 `--experimental-strip-types` 直接执行 TypeScript，无需编译步骤）：

```bash
npm test      # 运行 node:test 测试套件
npm run sample  # 运行 10000 tick 竞态复现样例
```

## 验收场景

固定种子 `0xC0FFEE`，5 节点集群运行 **10000 个离散时钟步长**：

1. `t=500` 注入网络分区：`{A,B}` 与 `{C,D,E}` 之间全断，且 `A<->B` 链路切断；
2. A、B 互为唯一候选探测目标，按同一协议周期镜像执行 → `t=680` 同时 suspect 对方 → **`t=880` 同时宣布对方崩溃（DECLARE_DEAD）**，极端竞争精确重现；
3. `t=4000` 分区愈合，双方各自收到"自己已死"的 gossip → incarnation 自增反驳（REFUTE）→ 高 incarnation 的 alive 记录在仲裁中击败 dead 记录 → 全集群收敛为一致的成员表；
4. 同种子重放两次，事件日志 sha256 完全一致。

## 运行样例（`npm run sample` 实际输出）

```
seed=0xc0ffee  ticks=10000  nodes=A,B,C,D,E
links: latency=2 jitter=1 loss=0 (partition injected at t=500, healed at t=4000)

--- deterministic event log ---
t=500 PARTITION_APPLIED  ({A,B} isolated from {C,D,E}, A<->B cut)
t=560 A DECLARE_SUSPECT C inc=0
t=560 B DECLARE_SUSPECT C inc=0
t=560 C DECLARE_SUSPECT B inc=0
t=560 D DECLARE_SUSPECT B inc=0
t=560 E DECLARE_SUSPECT B inc=0
t=600 A DECLARE_SUSPECT D inc=0
t=600 B DECLARE_SUSPECT D inc=0
t=640 A DECLARE_SUSPECT E inc=0
t=640 B DECLARE_SUSPECT E inc=0
t=680 A DECLARE_SUSPECT B inc=0
t=680 B DECLARE_SUSPECT A inc=0
t=680 C DECLARE_SUSPECT A inc=0
t=680 D DECLARE_SUSPECT A inc=0
t=680 E DECLARE_SUSPECT A inc=0
t=760 A DECLARE_DEAD C inc=0
t=760 B DECLARE_DEAD C inc=0
t=760 C DECLARE_DEAD B inc=0
t=760 D DECLARE_DEAD B inc=0
t=760 E DECLARE_DEAD B inc=0
t=800 A DECLARE_DEAD D inc=0
t=800 B DECLARE_DEAD D inc=0
t=840 A DECLARE_DEAD E inc=0
t=840 B DECLARE_DEAD E inc=0
t=880 A DECLARE_DEAD B inc=0     <-- 竞态：A 宣布 B 崩溃
t=880 B DECLARE_DEAD A inc=0     <-- 竞态：B 同时宣布 A 崩溃
t=880 C DECLARE_DEAD A inc=0
t=880 D DECLARE_DEAD A inc=0
t=880 E DECLARE_DEAD A inc=0
t=4000 PARTITION_HEALED
t=4002 B REFUTE inc=1
t=4002 D REFUTE inc=1
t=4003 C REFUTE inc=1
t=4003 E REFUTE inc=1
t=4003 A REFUTE inc=1
t=4004 C REVIVE D inc=1
...（全部成员互相 REVIVE，略）
--- final membership (node A view; all nodes identical) ---
  A: alive inc=1 declaredBy=A
  B: alive inc=1 declaredBy=B
  C: alive inc=1 declaredBy=C
  D: alive inc=1 declaredBy=D
  E: alive inc=1 declaredBy=E
--- network stats ---
  sent=7246 delivered=5919 dropped=1302
--- replay hash (sha256 of event log) ---
  47ad54b9560cbd04ef438d649ea61c9dd5380259a0d913b3872fa7c0d9c75624
```

## 测试结果（`npm test` 实际输出）

```
TAP version 13
# Subtest: race: A and B declare each other dead at the same tick; arbitration converges
ok 1 - race: A and B declare each other dead at the same tick; arbitration converges
# Subtest: deterministic replay: same seed reproduces the identical 10000-tick run
ok 2 - deterministic replay: same seed reproduces the identical 10000-tick run
# Subtest: arbitrate: total deterministic order over conflicting records
ok 3 - arbitrate: total deterministic order over conflicting records
# Subtest: seeded network: loss and jitter are reproducible and match the profile
ok 4 - seeded network: loss and jitter are reproducible and match the profile
# Subtest: healthy cluster: jitter and 2% loss cause no false positives in 10000 ticks
ok 5 - healthy cluster: jitter and 2% loss cause no false positives in 10000 ticks
1..5
# tests 5
# pass 5
# fail 0
# duration_ms ~500
```
