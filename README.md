# refdoc-store

多 JSON 文档存储：跨文档 ID 引用、版本化 schema、反向基数约束、原子批次事务。
零依赖 TypeScript，使用 Node.js 内置测试运行器（`node:test`），不借助任何现成验证器。

## 运行要求

- Node.js >= 22.18（利用内置 type stripping 直接运行 `.ts`，无需编译步骤、无需任何 npm 依赖）。

```sh
npm test        # node --test "test/*.test.ts"
npm run sample  # node samples/demo.ts
```

## 承诺的 schema 子集（只实现这些，其余一律拒绝）

```ts
{ kind: "string" | "number" | "integer" | "boolean" | "null" }
{ kind: "enum", values: unknown[] }
{ kind: "array", items: TypeNode, minItems?, maxItems? }
{ kind: "object", properties: {..}, required?: string[] }   // 未声明的属性报错
{ kind: "ref", targets: string[], maxIncoming?: number }
```

- 注册 schema 时若出现子集外的关键字（如 `pattern`、`oneOf`、`additionalProperties`），
  直接抛出 `unsupported-schema`，而不是静默忽略。
- schema 按 `name@version` 注册，多版本共存；文档可钉住 `schemaVersion`，缺省取最新版。
- `ref` 的值是字符串（目标文档 id）。`targets` 是允许的目标类型名；
  `maxIncoming` 是反向基数：同一目标经此规则节点最多被多少个不同文档引用。

## 事务语义（`store.transact(ops)`）

一个批次内的操作按序应用，整体原子提交：任一错误则全部回滚，文档与反向索引一起提交。

- `insert`：同批互引可通过存在检查（存在性按批次后状态判定）；重复 id（批内或与库中既有文档）冲突报错。
- `rename`：事务内重命名 id，引用图中所有指向旧 id 的引用被原子改写，反向索引同步更新。
- `delete`：仍被引用（且引用者不在同批删除之列）的对象拒绝删除；同批删除相互引用的对象合法。
- 循环引用合法；删除引用入口后，全量验证的错误计数为 0，反向索引无残留。

## 错误模型

每个错误同时携带：

- `instancePath`：出错数据在文档中的原路径（JSON Pointer）；
- `rulePath`：对应规则在 schema 规则树中的路径；
- `docs`：最小相关文档切片（只含涉案文档 id、路径与值；`path: "#"` 表示文档元信息切片）。

## 独立全量验证器

`validateAll(registry, docs)` 与存储的增量逻辑相互独立：从原始文档列表重新做数据校验、
存在性/目标类型/反向基数检查，并从头重建反向索引。`test/fuzz.test.ts` 用固定种子的随机
批次（合法/非法 insert、rename、delete 混合）持续对照：每个批次后全量验证器必须给出
0 错误、反向索引与增量维护结果完全一致、被拒绝的批次不得改变任何状态。

## 布局

- `src/schema.ts` — 子集定义、注册表、自研数据校验器（收集引用）
- `src/store.ts` — 文档存储、事务、增量反向索引
- `src/validator.ts` — 独立全量验证器
- `src/errors.ts` — 错误与文档切片类型
- `samples/demo.ts` — 运行样例
- `test/` — `node:test` 测试（schema、store、随机批次 fuzz 对照）
