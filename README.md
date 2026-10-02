# 列存字典动态扩容与热重构器

TypeScript 实现的字典编码列存组件：写时追加、字典随新词汇动态扩展；
当字典基数突破单字节上限（码字 > 255）时，自动把底层码字数组从
`Uint8Array` 热迁移为 `Uint16Array`（架构上可继续扩展到 32 位），
迁移同步原子完成，对外 `append` / `get` 服务不中断、结果一致。

## 环境要求

- Node.js >= 22.6（使用内置 `node:test` 与 `--experimental-strip-types`，零依赖、无需编译）

## 运行

```bash
npm test     # 运行 node:test 测试
npm run demo # 运行演示样例（可观察到热迁移日志）
```

## 设计要点

- `src/dictionaryColumn.ts`
  - `dictionary: string[]` + `Map<string, number>` 双向映射，O(1) 查词/查码；
  - 码字存储按位宽分档：`Uint8Array` → `Uint16Array` → `Uint32Array`；
  - `ensureWidthFor()`：新码字超出当前位宽时，分配更宽数组并整体重写已有位向量，
    记录并广播 `MigrationEvent`；同步执行，读写两侧始终看到一致状态；
  - `ensureCapacityFor()`：行容量倍增扩容，与位宽迁移正交；
  - `onMigration()`：订阅热重构事件，便于观测与审计。

## 验收覆盖（test/dictionaryColumn.test.ts）

- 初始小基数使用 uint8 存储；
- 连续写入 260 个新字符串，第 257 个新词（码字 256）触发 uint8→uint16 迁移，
  迁移恰好一次、重写当时全部 256 行位向量；
- 写入过程中持续抽样查询 + 迁移后全量回读，结果始终正确；
- 迁移后混合读写、容量倍增扩容、越界防护。
