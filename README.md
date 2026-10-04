# download-reassembly

版本化、崩溃安全的下载重组库（TypeScript，零依赖）。

## 特性

- **分段接收**：每个分段携带 `fileId`、`version` 和字节区间 `[offset, offset+length)`，允许重叠、乱序、重复投递。
- **重叠一致性校验**：新分段与已落盘的可信字节重叠的部分逐块比对；内容不一致时**隔离该版本**（`quarantined`），拒绝后续写入，且隔离状态持久化、重启后仍然生效。冲突不会写入任何新字节，磁盘上始终保留可信前缀。
- **摘要校验后发布**：覆盖完整 `[0, size)` 后流式计算全文件摘要，与注册时声明的 digest 一致才原子发布（rename + fsync）到 `published/`；不一致标记为 `failed`，绝不发布。
- **内存硬限额**：`maxMemoryBytes` 是库持有分段字节的硬上限（信号量 + 背压）；超过上限的单个分段自动**落盘**（spill 文件），之后按块处理。所有校验/摘要 I/O 均按 `chunkBytes` 分块，内存占用有界。
- **崩溃安全 checkpoint**：严格顺序为 *写数据 → fsync 数据文件 → 原子更新 checkpoint（tmp + rename + 目录 fsync）*。checkpoint 只记录已同步的覆盖区间；恢复时只信任 checkpoint，磁盘上未记录的字节一律忽略（重投时按普通重叠校验处理）。

## 目录布局

```
<dir>/
  data/         # 每个 (fileId, version) 一个 .part 稀疏数据文件
  checkpoints/  # 每个版本一个 JSON checkpoint（原子替换）
  spill/        # 超限分段的临时落盘区（重启即清空，不属于持久状态）
  published/    # 摘要校验通过后的最终文件
```

## 使用

```ts
import { ReassemblyStore } from 'download-reassembly';

const store = await ReassemblyStore.open({
  dir: './work',
  maxMemoryBytes: 8 * 1024 * 1024,
});

store.on('published', ({ info, path }) => console.log('published', info.version, path));
store.on('quarantined', ({ info }) => console.warn('quarantined', info.version, info.reason));

await store.registerFile({
  fileId: 'report.pdf',
  version: 'v3',
  size: 1_000_000,
  digest: { algorithm: 'sha256', hex: '...' },
});

const outcome = await store.addSegment({
  fileId: 'report.pdf',
  version: 'v3',
  offset: 0,
  data: chunk, // 调用方持有的 buffer 不会被复制；promise 结算前不要修改
});
// outcome: 'stored' | 'duplicate' | 'noop' | 'published' | 'failed'
```

版本状态机：`active → published | quarantined | failed`，后三者均为终态，继续投递会抛 `VersionNotWritableError`。

## 错误类型

- `SegmentOutOfBoundsError` — 区间越界（负偏移或超出声明的 `size`）
- `SegmentConflictError` — 重叠内容不一致（版本同时被隔离）
- `VersionNotWritableError` — 版本已处于终态
- `UnknownVersionError` / `SpecConflictError` — 未注册或重复注册冲突的 spec

## 运行测试

需要 Node ≥ 23.6（原生 TypeScript 类型擦除）：

```sh
npm test
```

如需类型检查，安装 `typescript` 与 `@types/node` 后运行 `npm run typecheck`。

## 测试覆盖

- 空文件（注册即完成校验并发布）
- 末端超界 / 负偏移 / 零长分段
- 乱序 + 重叠 + 重复分段的正确重组
- 重叠冲突隔离（含重启后隔离状态保持、不影响其他文件）
- 多版本交错投递、独立发布与隔离
- 重启后从 checkpoint 恢复并补齐缺口
- 模拟崩溃（数据已 fsync、checkpoint 未写）+ 撕裂写垃圾：恢复后不信任未记录数据
- 完整覆盖后摘要不匹配 → `failed`，不发布
- 内存硬限额：高水位不超过上限，超限分段走落盘路径
