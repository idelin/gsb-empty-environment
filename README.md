# 日志结构状态检查点生成器（lsm-checkpoint）

TypeScript 实现的状态机增量检查点组件：将内存状态树按页切片并逐页哈希，
用 worker 线程池并行生成只读快照；支持在持续写入的同时生成增量差量包；
恢复时沿清单哈希链逐块、逐页校验完整性，损坏页可被精确定位并拒绝。

## 设计

- **MVCC 状态树**（`src/stateTree.ts`）：`set()` 以不可变版本节点追加，
  快照在 seq S 处切出一致性只读视图，写入无需停顿；检查点完成后折叠历史版本。
- **页切片 + 页哈希**（`src/format.ts`）：快照按键排序切为定容页（默认 256 键/页），
  页哈希 = `sha256(pageId || payload)`；16 页组成一个分块文件，分块哈希 = `sha256(chunk 文件)`。
- **并行快照**（`src/checkpoint.ts` + `src/worker.ts`）：分块序列化与哈希在
  worker 线程池中并行完成，主线程只负责落盘。
- **增量差量包**：清单记录全部页/块哈希；块哈希与上一检查点相同则不重写文件，
  清单中以 `ref` 指向实际存储该块的检查点（内容寻址、只增不改）。
- **哈希链**：每份清单记录 `prevManifestHash = sha256(上一清单文件内容)`，
  恢复时从目标检查点回溯至创世清单逐环校验。
- **恢复校验**（`src/restore.ts`）：链校验 → 逐块哈希校验 → 逐页重新计算哈希校验；
  失败时抛出 `CorruptionError`，携带检查点 id、分块号、坏页 pageId、
  期望/实际哈希与键范围，坏数据一律拒绝。

## 目录结构

```
src/stateTree.ts    MVCC 内存状态树与一致性快照
src/format.ts       页/分块二进制格式与哈希
src/checkpoint.ts   检查点管理器（增量、worker 线程池、清单）
src/worker.ts       分块序列化/哈希 worker
src/restore.ts      哈希链校验恢复与坏页定位
test/checkpoint.test.ts  Node.js 内置测试（node:test）
demo/demo.ts        10 万键连续写入 + 检查点 + 损坏定位演示
```

## 运行

需要 Node.js >= 18（`node:test`）。TypeScript 3.9 与 @types/node 已离线
vendor 在 `node_modules/` 中，无需联网安装。

```bash
npm test     # 编译并运行 node:test 测试
npm run demo # 编译并运行端到端演示
```

## 测试结果（Node v22.14.0）

```
$ node --test dist/test/*.test.js
# tests 5
# pass 5
# fail 0
```

覆盖：写入并发下快照一致性、增量差量与哈希链、第 3 个分块损坏的精确定位与拒绝、
清单篡改断链检测、10 万键写入期间检查点的延迟抖动（p99 < 50ms，实测 p99 ≈ 0.003ms）。

## 演示输出样例

```
[phase 2] continuous writes to 100k keys, checkpoint triggered at 50k
  -> checkpoint cp-2 triggered at key 50000, writes continue
  cp-2: seq=50001 keys=50001 pages=196 chunks=13 stored=9 reused=4 bytesWritten=1048043 took=463ms
  incremental delta: only 9/13 chunks rewritten
  write latency before checkpoint : p50=0.000ms p95=0.001ms p99=0.002ms max=12.074ms
  write latency during checkpoint : p50=0.000ms p95=0.001ms p99=0.003ms max=14.761ms

[phase 5] corrupt the 3rd chunk stored by cp-3 and recover again
  corrupted 4 value bytes at offset 76 of cp-3/chunk-0012.bin (chunk index 12)
  recovery rejected bad data: checkpoint corruption detected in checkpoint cp-3, chunk 12 (...): 1 bad page(s): page 192
  bad page 192: keys [key-00049152 .. key-00049407] (256 keys)
    expected page hash: 235bcb818c07aea4... actual: 76ff6eb059fe53bb...

[phase 6] fall back to uncorrupted cp-2, recovery succeeds
  restored cp-2: seq=50001 keys=50001
```
