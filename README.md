# lsm-checkpoint — 日志结构状态检查点生成器

状态机增量检查点组件：将内存状态树按页哈希切片，并行生成只读快照；
支持在持续写入的同时生成增量差量包；恢复时对照哈希链校验数据完整性，
坏页 / 坏块可精确定位并拒绝加载。

## 设计

```
写入流 ──► StateTree (key 空间静态切片为 N 页, 页级哈希)
              │  checkpoint()  [与写入并发]
              ▼
         Checkpointer ── 逐页只读快照(周期性让出事件循环)
              │         ── 脏页筛选 → 增量差量包 (incr)
              │         ── 分块并行落盘 (Promise.all)
              ▼
   chunk-0001.bin ──► chunk-0002.bin ──► ...   块级链式哈希 H(i)=sha256(H(i-1)||chunk)
              │
   chk-XXXXXX.manifest.json                    清单级哈希链 H(m)=sha256(H(m-1)||manifest)
              │
              ▼
         restore() ── ① 校验清单哈希链 ② 逐块校验链式哈希(定位坏块序号)
                      ③ 逐页校验页哈希(定位 pageId) ④ 全部通过才应用数据
```

- **页切片**：`keyToPageId(key)`（FNV-1a）把 key 稳定映射到固定页，增量只需重放脏页；
- **并发快照**：单线程事件循环 + 每页同步序列化，快照无撕裂；序列化/编码循环周期性
  `setImmediate` 让出，写入延迟抖动受控（实测 p99 ≈ 0.004ms）；
- **两级哈希链**：分块之间、清单之间均为链式哈希，任何一字节篡改都会在
  对应层级被精确拒绝（`CheckpointCorruptionError` 携带 `chunkIndex` / `pageId`）。

## 目录

- `src/stateTree.ts` — 内存状态树：页切片、脏页追踪、页级只读快照
- `src/checkpointer.ts` — 全量 / 增量检查点生成，分块并行落盘，清单哈希链
- `src/recovery.ts` — 恢复与两级哈希链校验，坏块 / 坏页精确定位
- `src/hash.ts` — sha256、链式哈希、键→页映射
- `tests/checkpoint.test.ts` — node:test 验收测试
- `examples/demo.ts` — 端到端运行样例

## 运行

要求 Node.js ≥ 23.6（原生 TypeScript 类型剥离，仓库附 `.nvmrc`）。

```bash
nvm use            # 或任意 >= 23.6 的 node
npm test           # node --test "tests/*.test.ts"
npm run demo       # 端到端样例
```

## 测试结果（node --test）

```
✔ 增量检查点只包含脏页，恢复后状态一致
✔ 验收：连续写入 10 万键值期间触发检查点，写入延迟抖动受控
    [latency] baseline:          p50=0.001ms p99=0.013ms max=109.739ms
    [latency] with checkpoint:   p50=0.001ms p99=0.005ms max=36.558ms
✔ 验收：损坏第 3 个分块，恢复流程精确定位并拒绝坏数据
    [corruption] precisely located: chunk #3 of checkpoint chk-000001 failed
    hash-chain verification (file=chk-000001-chunk-0003.bin, pages=[8,9,10,11])
✔ 清单被篡改时哈希链校验失败
ℹ pass 4  ℹ fail 0
```

延迟判定方式：与无检查点的基线写入对比 —— 并发检查点不引入额外抖动
（p99 均在微秒级；max 离群点为环境噪声，基线同样出现，断言要求
`max ≤ max(50ms, 3 × baselineMax)`）。

## 样例输出（npm run demo）

```
== 1. 连续写入 100,000 个键值对，中途触发全量检查点 ==
   基线(无检查点)  : p50=0.001ms p99=0.009ms max=62.699ms
   写入+并发检查点 : p50=0.001ms p99=0.008ms max=81.441ms
   检查点 chk-000001: 32 个分块, manifestHash=bbd73ea1901dd02d…
== 2. 再修改 1,000 个键，生成增量差量包 ==
   增量 chk-000003 (incr): 仅 584 个脏页 / 19 个分块
== 3. 正常恢复并校验哈希链 ==
   恢复成功: 100000 个键, 校验 83 块 / 2632 页
== 4. 损坏第 3 个分块后重试恢复 ==
   恢复拒绝坏数据 ✔  定位: checkpoint=chk-000001 chunk=#3 file=chk-000001-chunk-0003.bin
```
