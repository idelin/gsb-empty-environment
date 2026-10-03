# 列存字典动态扩容与热重构器

TypeScript 实现的字典编码列存组件：写时追加、字典随新词汇动态扩容；
当字典基数突破单字节门限（编码 id > 255）时，自动将底层位向量从
`uint8` 热迁移到 `uint16`（再到 `uint32`），迁移采用
“构建新向量 → 拷贝 → 原子换引用”，读写服务全程不中断。

## 结构

- `src/dictionary.ts` — 字符串 ↔ 编码 id 双向映射，append-only
- `src/column.ts` — `DictionaryEncodedColumn`：编码位向量存储、动态扩容、热重构核心
- `test/column.test.ts` — Node.js 内置 `node:test` 测试（8 个用例）
- `examples/demo.ts` — 运行样例：连续写入 260 个新字符串

## 运行要求

Node.js ≥ 22.6（利用原生 TypeScript type-stripping 直接运行 `.ts`，零依赖、零构建；
推荐 ≥ 23.6，type stripping 默认开启）。

```bash
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 验收结果

`node --test` 输出（8/8 通过）：

```
✔ 初始状态：空列使用 uint8 存储
✔ 低基数写入：保持 uint8，重复值不扩张字典
✔ 门限边界：256 个不同词汇仍用 uint8，第 257 个触发升级
✔ 验收场景：连续写入 260 个新字符串，自动升级且查询持续正确
✔ 升级后仍可继续写入与读取（服务不中断）
✔ 位向量重写不丢数据：升级瞬间既有编码逐位一致
✔ 越界访问抛出 RangeError
✔ 统计信息反映存储层状态
ℹ pass 8  ℹ fail 0
```

`npm run demo` 关键输出：

```
初始存储位宽: uint8
写入 101 行后: 位宽=uint8, 字典基数=101
[热重构] uint8 -> uint16（触发时字典基数=257, 已有行数=256）
写入 260 行后: 位宽=uint16, 字典基数=260
get(0)   = word-0000
get(255) = word-0255
get(256) = word-0256
get(259) = word-0259
```

## 设计要点

- **门限**：uint8 可表示编码 id 0–255，即 256 个不同词汇仍在界内；
  第 257 个新词汇获得 id 256，突破 255 门限，触发升级。
- **热重构**：`reencode()` 先在后台构建更宽的新位向量并完整拷贝既有编码，
  再一次引用交换完成切换；切换前后任意时刻查询结果均正确。
- **可观测**：`onUpgrade()` 订阅升级事件，`stats()` / `upgrades` 暴露
  位宽、字典基数、升级次数与内存占用。
