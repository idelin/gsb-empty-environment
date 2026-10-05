# download-reassembly

分段下载重组库（TypeScript，零运行时依赖）。接收带 **文件版本 + 字节区间** 的分段，
允许重叠、乱序、重复投递；重叠内容不一致时隔离（quarantine）该版本；完整覆盖后校验
全文件 SHA-256 摘要再发布。内存有硬限额，超出自动落盘；checkpoint 只记录已 fsync 的数据。

## 运行要求

- 测试：Node ≥ 23.6（原生 TypeScript 类型擦除），或 Node ≥ 22.6 加 `--experimental-strip-types`
- 类型检查：任意 tsc 5.x（`tsconfig.json` 中的 `typeRoots` 指向本机 `@types/node`，请按环境调整）

```sh
npm test          # node --test 'test/*.test.ts'
npm run typecheck # tsc --noEmit
```

## API 概览

```ts
import { ReassemblyStore } from './src/reassembly.ts';

const store = await ReassemblyStore.open('/var/dl/store', { maxBufferedBytes: 8 << 20 });

// 声明期望的版本：总大小 + 全文件 SHA-256（十六进制）
await store.declareFile('app.bin', /*version*/ 3, /*size*/ 1_000_000, /*sha256*/ '…');

// 分段到达（任意顺序、可重叠、可重复）
const result = await store.addSegment('app.bin', 3, /*offset*/ 4096, chunk);
// result ∈ 'stored' | 'duplicate' | 'published'
//         | 'quarantined-conflict' | 'failed-digest'
//         | 'rejected-out-of-bounds' | 'rejected-not-receiving' | 'rejected-unknown-version'

store.getVersionInfo('app.bin', 3);   // { state, receivedBytes, gaps, … }
await store.flush();                  // 显式落盘 + checkpoint（崩溃恢复点）
store.publishedPath('app.bin');       // 发布产物路径
```

## 语义

- **区间越界**：`offset + length > size`（或负偏移）的分段被拒绝，不影响已有状态。
- **重叠一致性**：新分段与已接收区间重叠时逐字节比对（内存脏页优先，否则读盘）。
  内容一致 → 幂等接受；不一致 → 该版本立即 `quarantined`，数据文件删除，后续分段拒收。
  隔离以版本为单位，同文件的其他版本不受影响。
- **发布**：覆盖率达成 `[0, size)` 后，先把全部脏数据 fsync 落盘，再流式计算全文件
  SHA-256 与声明值比对；一致则原子 rename 到 `published/`，否则版本标记 `failed`，不发布。
- **空文件**：`size = 0` 在 `declareFile` 时立即校验空内容摘要并发布（或失败）。
- **内存硬限额**：全局脏字节数超过 `maxBufferedBytes` 即触发落盘；单分段超过限额时
  直接 write-through 写盘，不进内存缓冲。

## 持久化与崩溃恢复

```
<dir>/
  checkpoint.json          # 原子写入（tmp + fsync + rename + 目录 fsync）
  data/<hex(fileId)>.<version>.part   # 稀疏数据文件
  published/<hex(fileId)>             # 最新发布版本的内容
```

- **checkpoint 不信任未同步数据**：只有写入数据文件并 fsync 之后的区间才会进入
  `checkpoint.json`。未 flush 的内存数据在崩溃后表现为缺口（gap），重传即可补齐。
- 写盘顺序固定为：数据 → fsync → checkpoint，因此 checkpoint 中的区间必然有对应
  的已持久化字节；恢复时若数据文件意外缺失，防御性地将区间重置为缺口而不是信任它。
- 所有变更操作在内部串行化（promise 队列），异步磁盘 IO 不会交错。

## 测试覆盖（`test/reassembly.test.ts`，17 例）

空文件（摘要匹配/不匹配）、末端超界、乱序+重叠+重复重组、重叠冲突隔离、
相同重传不算冲突、覆盖完整但摘要错误、多版本交错独立发布、单版本隔离不影响其他版本、
flush 后恢复并补齐缺口、未 flush 数据不进 checkpoint、已发布状态可恢复、
内存限额触发落盘、超大单分段 write-through、冲突检测覆盖已落盘数据、
重复声明幂等、未声明版本拒收。
