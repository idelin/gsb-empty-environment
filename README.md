# LSM 写路径：WAL + MemTable → Immutable → Level-0 SSTable 刷盘状态机

TypeScript 实现的 LSM 树内存表向 SSTable 持久化转换状态机，零依赖，使用 Node.js 内置
`node:test` 测试（利用 Node ≥ 23.6 的原生 TypeScript 类型擦除直接运行 `.ts`）。

## 状态机

```
put ──> WAL append ──> Active MemTable
                              │ size >= 4MB 阈值
                              ▼
                    冻结为 Immutable MemTable（O(1) 交换，前台不停顿）
                    ├── 密封旧 WAL 段（fsync + close）
                    └── 派生新 MemTable + 新 WAL 段接收写入
                              │
                              ▼
              后台单线程顺序刷盘（单条异步链，FIFO）
                    Immutable ──> L0 SSTable（fsync）──> 安全删除已密封 WAL
```

MemTable 生命周期状态：`active → frozen → flushing → flushed`。

## 组件

| 文件 | 职责 |
| --- | --- |
| `src/wal.ts` | 预写日志段：CRC32 校验记录、密封轮转、崩溃重放（损坏尾部截断） |
| `src/memtable.ts` | 内存表：字节级大小核算、阈值检测、墓碑（删除）支持 |
| `src/sstable.ts` | L0 SSTable：写入（含 CRC 页脚 + magic + fsync）与读取 |
| `src/engine.ts` | 状态机引擎：轮转、后台顺序刷盘、读路径（active→immutable→L0）、崩溃恢复 |

## 运行

```bash
node --version          # 需要 >= 23.6（或 22.6+ 加 --experimental-strip-types）
npm test                # node --test test/*.test.ts
npm run demo            # node examples/demo.ts
```

Node 22.x 备选：`node --experimental-strip-types --test test/*.test.ts`

## 验收结果（实测）

- **两次 4MB 阈值触发**：高频写入 1KB value，memtable#0/#1 先后在 4.00MB 冻结，
  顺序刷入 `l0-000000.sst`、`l0-000001.sst`（FIFO，单线程无并发刷盘）。
- **前台无停顿**：刷盘期间写入持续接收，put 路径不等待 flush（测试用可控制的
  flush 闸门确定性验证；demo 实测最大 put 延迟 61.6ms，仅为轮转时的 WAL fsync 交接）。
- **数据完整**：8468/8468 键全量校验通过；WAL 段在 SSTable fsync 后才删除，
  崩溃恢复测试（`close({flush:false})` 后重放 WAL）通过。
- **测试**：`node --test` 11/11 通过（WAL 3 + MemTable 3 + 引擎 5）。
