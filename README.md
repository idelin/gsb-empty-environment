# 去中心化集群拓扑视图分歧检测与校验和快速比对

多节点成员状态一致性审计器（TypeScript，零依赖，Node.js 内置测试运行器验证）。

## 设计

- **增量哈希环 Digest**：每个成员条目贡献 `crc32("id|status|incarnation")`，
  全表 Digest 为所有条目校验和的 XOR——增删改均为 O(1)，且与插入顺序无关。
  成员 ID 空间映射到 16 个哈希环区间（bucket），每个区间维护独立的增量 Digest。
- **周期探活比对**：探活时双方仅交换 **4 字节 CRC Digest**；相等即视图一致，
  一次探活只花 8 字节。
- **区间状态对账**：Digest 失配时交换 16 区间 Digest 映射（128 字节），精确定位
  分歧区间，仅拉取分歧区间内的成员记录做双向合并（incarnation 大者胜，
  同 incarnation 按 `dead > left > suspect > alive` 决胜）。
- **对账轮次**：SWIM 风格，每轮每节点探测一个对等节点（轮转），
  分区愈合后通过桥接链路逐步向全簇传播。

## 文件

- `src/crc32.ts` — CRC-32 (IEEE) 4 字节摘要原语
- `src/membership.ts` — 成员表 + 增量哈希环 Digest + 合并规则
- `src/node.ts` — 节点：探活 / Digest 比对 / 区间对账协议
- `src/simulation.ts` — 分区愈合场景模拟（`npm run demo`）
- `test/` — Node.js 内置 `node:test` 测试（`npm test`）

## 运行

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）：

```sh
npm test       # node --test（内置测试运行器）
npm run demo   # 分区愈合模拟
```

## 运行样例（npm run demo）

```
=== partition-heal divergence detection simulation ===
[baseline] 6-node cluster, uniform digest=3967b15b
[partition] links cut: {L1,L2,L3} x {R1,R2,R3}
[partition] left digest=7ed6b71d right digest=746c4902 (diverged)
[heal] bridge links restored: L1<->R1, L3<->R2
[round 1] R1->L1 MISMATCH buckets=[3,6,12,15] pull=3 push=2; R2->L3 MISMATCH buckets=[3,6,12,15] pull=3 push=2; R3->R1 MISMATCH buckets=[3,6,12] pull=3 push=0
[round 1] digests L1:990951ba L2:7ed6b71d L3:990951ba R1:990951ba R2:990951ba R3:990951ba
[round 2] L2->L3 MISMATCH buckets=[12,15] pull=2 push=0
[round 2] digests L1:990951ba L2:990951ba L3:990951ba R1:990951ba R2:990951ba R3:990951ba
[converged] all 6 nodes aligned after 2 round(s), digest=990951ba

rounds used        : 2 (limit 5)
mismatches detected: 4
digest bytes moved : 608
record bytes moved : 386
records pulled     : 11
records pushed     : 4

final membership view on every node (digest=990951ba):
  L1   alive    inc=3
  L2   alive    inc=2
  L3   alive    inc=1
  L4   alive    inc=1
  R1   alive    inc=1
  R2   alive    inc=1
  R3   alive    inc=3
  R4   alive    inc=1
```

验收边界达成：两子集群（各 3 节点）分区期间各自演化成员视图（新成员加入、
自辩升 incarnation、误报 suspect），愈合瞬间通过 4 字节 CRC Digest 交换
立即察觉不一致，触发区间对账与差异拉取，**2 轮内（≤5 轮上限）完成全量双向对齐**，
6 节点 Digest 与成员快照完全一致。

## 测试结果（npm test）

```
✔ crc32 matches the IEEE check vector
✔ crc32 of empty input is zero
✔ crc32 accepts strings and bytes identically
✔ crc32 is deterministic and input-sensitive
✔ digestToHex renders 4 bytes as 8 hex chars
✔ incremental digest always equals a from-scratch recompute
✔ digest is order-independent (commutative ring checksum)
✔ diverged views produce different 4-byte digests
✔ merge rule: higher incarnation wins
✔ merge rule: equal incarnation breaks ties by status rank
✔ stale upserts are rejected and leave the digest untouched
✔ bucket digests locate exactly the diverging intervals
✔ in-sync probe costs only the 4-byte digest exchange
✔ digest mismatch triggers interval reconciliation and aligns both nodes
✔ reconciliation transfers only records inside differing buckets
✔ probing an unconnected peer is rejected
✔ partition heal: full bidirectional alignment within 5 rounds
ℹ tests 17
ℹ pass 17
ℹ fail 0
```
