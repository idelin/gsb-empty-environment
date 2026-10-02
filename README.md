# json-ref-repo

多 JSON 文档仓库：文档通过全局唯一 ID 互相引用，支持**批量原子提交**（同批文档可相互引用，含循环引用）、**事务内重命名**、**反向基数约束**与**版本化 schema**。纯 TypeScript，零依赖，使用 Node.js 内置测试运行器（`node --test`，Node ≥ 23 直接运行 `.ts`）。

## 承诺实现的 schema 子集（仅这些，不借任何现成验证器）

- 对象类型（`types.<Type>.fields`），字段种类：`string` / `number` / `boolean` / `ref` / `ref[]`
- 字段可选/必填（`required`）
- `ref` / `ref[]` 的目标类型（`target`），按**引用方文档自己的 schema 版本**判定
- 目标类型上的反向基数：`incoming: { min, max }`（统计所有指向该文档的引用数）
- 多版本 schema 注册表（`SchemaRegistry`），每个文档声明自己的 `schemaVersion`

未列出的 JSON Schema 特性（pattern、oneOf、additionalProperties……）一律不承诺。

## 文档模型

```ts
interface Document {
  id: string;            // 全仓库唯一
  type: string;          // 类型名，在该文档的 schemaVersion 中解析
  schemaVersion: string; // 判定本文档（及其出边引用规则）的 schema 版本
  data: Record<string, unknown>;
}
```

## 事务 API（`src/repository.ts`）

```ts
const repo = new Repository(schemas);
repo.batch((tx) => {
  tx.put(docA);            // 插入或整体替换；同批同 ID 不同内容 => DuplicateId 冲突
  tx.put(docB);            // docA/docB 可互相引用：存在性按“批后状态”检查
  tx.rename("old", "new"); // 事务内重命名：重写所有（含批内）指向 old 的引用
  tx.delete("id");         // 仍被存活文档引用 => DeleteReferenced，整批回滚
});
```

- 提交先在候选状态上完整校验，**任何错误 → 全部不应用**（文档与反向索引一起原子切换）。
- 循环引用合法；删除循环的全部入口后无残留错误、反向索引为空。
- 反向索引：`target -> (source -> 引用出现的 instancePath 集合)`，提交时与文档一起原子更新。

## 错误模型（`src/errors.ts`）

每个错误同时给出**实例路径**与**规则路径**，并附**最小相关文档切片**：

```ts
interface ValidationError {
  code: ErrorCode;       // MissingTarget | TargetTypeMismatch | ReverseCardinality |
                         // DuplicateId | DeleteReferenced | RenameConflict | ...
  docId?: string;
  instancePath: string;  // 文档内原始路径，如 "/data/watchers/0"
  rulePath: string;      // schema 内规则路径，如 "/types/Task/fields/assignee"
  message: string;
  slice: unknown;        // 出错位置的最小子值；反向规则则为引用方 {docId, paths} 列表
}
```

## 独立全量验证器（`src/validator.ts`）

`validateAll(docs, schemas)` 不复用仓库的提交校验与索引构建，从原始文档集独立重算全部规则与反向映射。`tests/crosscheck.test.ts` 用固定种子的随机批次（put/rename/delete/upsert/非法操作混合，300 批）对照：

- 提交被接受 ⇒ 全量验证器对当前状态报 0 错误，且反向索引与验证器独立计算的完全一致；
- 提交被拒绝 ⇒ 仓库状态逐字节不变，且仍为有效状态。

## 运行

```sh
npm test     # node --test "tests/**/*.test.ts"
npm run sample
```

## 运行样例（`npm run sample` 输出节选）

```
=== 3. Rename inside a transaction rewrites all references ===
  commit: {"ok":true}
  t1.data.assignee = user-ada
  t2.data.watchers = ["user-ada"]

=== 4. Deleting a still-referenced document is rejected ===
  [DeleteReferenced] doc=user-ada at /id (rule /documents/id): cannot delete "user-ada": still referenced by 2 document(s)
    slice: [{"docId":"t1","paths":["/data/assignee"]},{"docId":"t2","paths":["/data/watchers/0"]}]

=== 5. Versioned schema decides the target type ===
  [TargetTypeMismatch] doc=t3 at /data/assignee (rule /types/Task/fields/assignee): reference expects target type "Account" but "u2" is of type "User"
    slice: {"targetId":"u2","expected":"Account","actual":"User"}
  with an Account target: {"ok":true}

=== 7. Cycles are legal; deleting entry points leaves zero residual errors ===
  insert a<->b cycle: {"ok":true}
  delete all entry points: {"ok":true}
  residual errors after teardown: 0, repo size: 0
```

## 测试结果（`npm test`，Node v23.9.0）

```
cross-check: 300 random batches -> 139 accepted, 161 rejected, 115 renames, 106 deletes; repo+validator agree everywhere
✔ independent full validator cross-checks random batches
✔ reference cycles are legal and leave no residual errors after entry-point deletion
✔ batch atomic insert: mutual references in the same batch pass existence checks
✔ deleting referenced entry points in one batch leaves zero residual errors
✔ duplicate id with different documents in one batch conflicts; identical content is idempotent
✔ deleting a still-referenced document is rejected and the batch is atomic
✔ rename inside a transaction rewrites references and the reverse index
✔ reverse cardinality min/max is enforced with schema rule path and referrer slice
✔ target type is judged by the referencing document's schema version
✔ errors carry instance path, rule path and the minimal relevant slice
✔ put replaces an existing document and updates the reverse index
✔ unknown type / unknown schema version are reported
ℹ tests 12  ℹ pass 12  ℹ fail 0
```
