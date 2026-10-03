# ring-pool — 定长环形内存池 + 零拷贝切片复用器

高性能网络 IO 内存缓冲区管理器（TypeScript / Node.js，零依赖）。

## 设计

```
                capacity（固定，如 4MiB）
  ┌──────────────────────────────────────────────┐
  │###########|----------------------|###########│  arena: 进程级唯一大块 Buffer
  └──────────────────────────────────────────────┘
   ^pinPos     ^readPos              ^writePos
   回收水位     未读帧起点            写入位置
```

- **定长环形空间**：构造时分配一块固定大小的 `Buffer`（`arena`），生命周期内不扩容、不释放；逻辑位置单调递增，物理偏移 `pos % capacity`。
- **帧流模型**：生产者 `writeFrame(data)` 追加帧（唯一的拷贝点：数据进环）；消费者 `readFrame()` 切出下一帧的 `Slice`。帧若跨越环尾，写入侧自动插入 padding（有界：单帧最大长度），保证每帧物理连续。
- **零拷贝切片**：`Slice` 只持有「偏移 + 长度」引用，`view()` 直接返回环形区子视图，不发生任何内存拷贝。
- **引用计数回收**：切片初始计数为 1，`retain()` / `release()` 增减；归零时切片失效并归还对象池，其钉住的环形区间按 FIFO 水位（`pinPos`）释放。乱序释放安全：水位不越过最老未释放切片。
- **背压**：环形空间不足时 `writeFrame` 返回 `false`，由调用方消费后重试，绝不覆盖未释放数据。
- **近零 GC**：`Slice`、帧描述符、钉住节点全部对象池复用；稳态下每帧的堆分配为零。

## API

| 成员 | 说明 |
| --- | --- |
| `new RingPool(capacity)` | 创建容量为 `capacity` 字节的环形池 |
| `pool.writeFrame(data)` | 追加一帧；空间不足返回 `false`（背压） |
| `pool.readFrame()` | 切出下一帧的只读 `Slice`；无帧返回 `null` |
| `slice.view()` | 零拷贝只读视图（与环形区共享物理内存） |
| `slice.get(i)` / `slice.copyTo(t)` / `slice.toBuffer()` | 零分配读字节 / 拷出 |
| `slice.retain()` / `slice.release()` | 引用计数 +1 / -1，归零回收 |
| `pool.used` / `pool.free` / `pool.activeSlices` | 占用 / 剩余 / 在途切片数 |
| `pool.framesRead` / `pool.slicesReleased` / `pool.slicesAllocated` | 守恒与复用统计 |

## 运行

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译与依赖安装）。

```bash
npm test        # node --test（内置测试运行器）
npm run demo    # node --expose-gc examples/demo.ts
```

## 测试结果（node --test，8/8 通过）

```
✔ 单帧写入/读出，内容零拷贝一致
✔ 零拷贝：切片视图与环形内存共享同一物理缓冲
✔ 物理回绕：帧跨界时自动 padding，内容保持正确
✔ 背压：空间不足返回 false，释放切片后空间回收
✔ 引用计数：retain/release 严格守恒，重复释放抛错
✔ 乱序释放：水位不越过最老未释放切片
✔ 非法输入：空帧与超容量帧抛错
✔ 验收：100MB 随机帧流经过 4MB 环形池，切片守恒且对象分配有界
    [stress] frames=3201 peakUsed=4.00MiB slicesAllocated=1 padding=513603B
ℹ pass 8
ℹ fail 0
```

## 运行样例（100MiB 随机帧流 @ 4MiB 环形池）

```
========== 环形内存池 · 运行样例 ==========
流量总量        : 100.00 MiB (3229 帧, 64B~64KiB 随机)
环形常驻空间    : 4.00 MiB (运行峰值占用 4.00 MiB, padding 704980 B)
吞吐            : 2941 MiB/s (耗时 34 ms, 含随机帧生成与校验)
---------- 零拷贝与守恒 ----------
帧校验          : 100.00 MiB 逐字节比对通过（切片视图引用环形区，全程无拷贝)
切片借出/释放   : 3229 / 3229 (终态在途 0)
切片对象复用    : 正式运行新分配 0 个 Slice 对象服务 3229 帧 (峰值在途 64)
---------- GC 与堆开销（稳态） ----------
运行期 GC       : major +4, minor +4 / 100MiB 流量
heapUsed 变化   : 8.00 MiB -> 8.08 MiB (Δ 0.08 MiB)
external 变化   : 11.12 MiB -> 16.25 MiB (Δ 5.14 MiB, Buffer 堆外内存)
========== 全部验收断言通过 ==========
```

## 验收边界核对

| 验收项 | 结果 |
| --- | --- |
| 连续流入 100MB 随机帧流 | ✅ 3229 帧 / 100MiB，逐字节校验通过 |
| 内存池常驻仅占 4MB 环形空间 | ✅ `arena` 固定 4MiB，峰值占用 4.00MiB，不随流量增长 |
| 切片借用与释放严格守恒 | ✅ 借出 3229 == 释放 3229，终态在途 0；重复释放/释放后访问抛错 |
| 无内存泄漏 | ✅ 稳态 heapUsed Δ 0.08MiB；连续 3 轮 100MiB 后 rss 收敛（129.9 → 135.9 → 136.1 MiB），首轮增长为分配器一次性预热 |
| GC 开销基本为零 | ✅ 正式运行新分配 0 个 Slice 对象（对象池复用）；100MiB 流量仅 major GC +4 / minor +4 |

## 目录

```
src/ring-pool.ts        核心实现（RingPool + Slice）
test/ring-pool.test.ts  node:test 内置测试（8 项）
examples/demo.ts        100MiB 流量运行样例（含 GC/内存观测）
```
