# ring-mempool

高性能网络 IO 内存缓冲区管理器：**定长块环形内存池** + **零拷贝切片复用器**。

## 设计

- **RingBufferPool**（`src/pool.ts`）：一次性预分配 `blockCount × blockSize`（默认 64 × 64KB = 4MB）连续内存，写入方按环形顺序填充。常驻内存恒等于池容量，与流量无关。
- **Slice**（`src/slice.ts`）：对池内内存的只读零拷贝引用（buffer + offset + length），引用计数生命周期管理；计数归零时回收底层块，且 Slice 对象本身归还对象池复用，GC 开销趋近于零。
- **背压**：写头追上仍被引用的块时抛出 `PoolExhaustedError`，调用方可据此限流；`writableBytes` / `canWrite(n)` 可预先查询。
- **约束**：单帧不超过块大小（切片不跨块，回收按块计数即可）。

## 用法

```ts
import { RingBufferPool } from "./src/pool.ts";

const pool = new RingBufferPool(); // 4MB 环形池

// 生产者：收包后写入，得到零拷贝切片
const slice = pool.write(incomingBytes);

// 消费者：直接在池内内存上解析，不做拷贝
parse(slice.view);

// 可转交他人（引用计数 +1）
const shared = slice.retain();

// 用完释放；归零后底层块安全回收
slice.release();
shared.release();
```

## 运行

```bash
npm test    # Node.js 内置测试（node:test），含 100MB 流验收用例
npm run demo # 运行样例：100MB 随机帧流穿过 4MB 环形池
```

要求 Node.js >= 23.6（原生 TypeScript type stripping，无需构建）。

## 验收结果

- 100MB 随机帧流（6266 帧，512B~32KB）连续流过，读写双端 FNV-1a 哈希一致；
- 池常驻内存固定 4MB；
- 结束时 `outstandingSlices = 0`、`liveReferences = 0`，借用/释放严格守恒；
- 全程仅创建 1 个 Slice 对象（对象池复用 6265 次），堆增长 < 2MB，GC 开销基本为零。
