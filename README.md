# json-ref-store

多文档 JSON 存储：文档通过 ID 互相引用，提供批量原子事务、反向索引、
反向基数约束与版本化 schema。纯 TypeScript，零依赖，使用 Node.js 内置
测试运行器（Node >= 23，直接以类型擦除方式运行 `.ts`）。

## 运行

```sh
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## 承诺的 schema 子集（仅这些，不借任何现成验证器）

- `scalar`：`"string" | "number" | "boolean"`
- `ref`：单个 ID 引用，声明 `target` 类型
- `refArray`：ID 引用数组，声明 `target` 类型
- `object`：内联嵌套对象（自有 `properties` / `required`）
- `TypeDef.required`：必填属性
- `TypeDef.maxIncoming`：反向基数约束（某类型文档最多被引用多少次）
- 未声明的额外属性允许存在，不参与校验

## 语义

- **批量原子新增**：同一事务内插入的文档可互相引用（含循环引用），
  存在性检查在整批应用后统一进行；任一错误则整批回滚。
- **事务内重命名**：`rename` 原子地改写所有引用方的引用值与反向索引
  （含自引用）；目标 ID 已存在则 `RENAME_CONFLICT`。
- **删除约束**：仍被引用的文档删除被拒绝（`DELETE_REFERENCED`）；
  同批删除引用入口与目标则合法，删除循环后错误计数归零、反向索引无残留。
- **重复 ID**：同 ID 不同内容必须冲突（`DUPLICATE_ID`）；内容完全相同的
  重复插入是幂等 no-op。
- **版本化 schema**：`setSchema` 用当前全部文档对新版本做全量校验，
  失败则保留旧版本；目标类型按当前生效的 schema 版本判定。
- **错误模型**：每个错误同时给出实例路径（`instancePath`，JSON Pointer）
  与规则路径（`schemaPath`，如 `#/types/book/properties/author`），
  并附带 `slice` —— 实例路径处的最小相关文档切片。
- **独立全量验证器**：`src/validator.ts` 与存储的提交期检查是两套独立
  实现；随机批次对照测试（`test/fuzz.test.ts`，固定种子）在每轮后比较
  增量维护的反向索引与验证器从零重建的索引，并验证拒绝批次的原子性。

## 文件

- `src/schema.ts` — 版本化 schema 子集类型
- `src/store.ts` — 事务存储（insert / rename / delete）、反向索引
- `src/validator.ts` — 独立全量验证器
- `src/errors.ts` / `src/pointer.ts` — 错误模型与 JSON Pointer 工具
- `examples/demo.ts` — 运行样例
- `test/store.test.ts` — 语义测试；`test/fuzz.test.ts` — 随机批次对照
