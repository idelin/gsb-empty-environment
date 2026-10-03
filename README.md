# LSM Tombstone 内核

LSM 树删除语义管理内核（TypeScript 实现，零依赖）：

- **Tombstone 标记**：`delete(key)` 不物理删除，而是写入 `value = null` 的特殊标记（携带全局递增 `seq`）。
- **读路径屏蔽**：Point Get 按 MemTable → L0（新文件优先）→ L1…Ln 的顺序查找，命中 Tombstone 立即返回 `NotFound`；Range Scan 按 `seq` 合并所有层的最新版本后过滤 Tombstone。
- **安全物理消除**：Compaction 合并时按 key 保留最大 `seq` 的条目；仅当目标层是 **Bottom-most（最底层，更深层无任何 SSTable）** 时才丢弃 Tombstone，否则必须保留下沉，防止更深层残留的旧版本"复活"。

## 结构

| 文件 | 职责 |
| --- | --- |
| `src/entry.ts` | Entry / TOMBSTONE 常量 / 多路归并 `mergeEntries` |
| `src/memtable.ts` | 可变 MemTable（有序输出） |
| `src/sstable.ts` | 不可变 SSTable，落盘为 JSON 文件，合并后旧文件 `unlink` 释放空间 |
| `src/lsm.ts` | `LSMTree`：put/delete/get/scan/flush/compact，层级容量级联压实 |

## 运行

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除，无需编译）：

```bash
npm test    # node --test（Node 内置测试运行器）
npm run demo
```

## 验收流程（tests/lsm.test.ts 与 examples/demo.ts 均覆盖）

1. `put K1=V1` → flush → 逐层压实至最底层 L3；
2. `delete K1` 写入 Tombstone → `get(K1)` 立即返回 `undefined`（NotFound）；
3. Tombstone 经多轮 Compaction 下沉 L0→L1→L2，期间因更深层存在旧值 `V1`，Tombstone 被保留；
4. 合并进入最底层 L3 时，Tombstone 与旧值 `V1` 一并物理擦除，磁盘文件中不再出现 `K1` 的任何痕迹。

## 测试结果

```
✔ 删除后立即查询返回 NotFound（MemTable 中的 Tombstone 屏蔽）
✔ Tombstone 落盘后继续屏蔽 SSTable 中的旧值
✔ Range Scan 跳过 Tombstone 与被覆盖的旧版本
✔ 删除后重新写入可复活该 Key（seq 决定新旧）
✔ Compaction 丢弃被覆盖的旧版本（非最底层也生效）
✔ 验收流程：多轮 Compaction 后 Tombstone 在最底层被物理擦除
✔ 删除不存在的 Key：Tombstone 到达最底层后同样被擦除
✔ 自动 Flush/Compaction 下与模型一致的随机一致性测试
ℹ pass 8 / fail 0
```
