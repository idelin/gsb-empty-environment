# graph-store

跨文档 `$id`/`$ref` 对象图存储：共享身份保持、可逆内联导出、局部更新、
按根裁剪导出、写时复制、反向引用与可回收集合、版本化快照（旧版本读者不受
新版本发布失败影响）。输入为 JSON 兼容树；对象键无序、数组有序；所有错误
都携带具体路径（如 `/docs/docA/link`）。

## 运行要求

Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

```sh
npm test
# 或显式：
node --experimental-strip-types --test 'test/*.test.ts'
```

## 设计

- **身份 ≠ 内容**：每个对象/数组节点在 `Arena` 中获得唯一 `NodeId`；
  内容相同的两个节点仍是不同身份（测试用身份关系矩阵验证）。
- **两阶段加载**：先分配身份并登记 `$id`，再解析 `$ref` 边，因此循环
  引用天然由显式回引（`$id` + `$ref`）表示。
- **引用即符号边**：`$ref` 槽位只存名字，按版本的 `idTable` 解析。删除
  文档后引用自动变为 *pending*（可查询、可定位），绝不补 `null`。
- **写时复制**：`draft.set()` 沿路径克隆容器；穿过 `$ref` 边写入时在该
  引用点物化共享子图的私有深拷贝（剥掉 `$id`），其他根保持原身份。
- **发布校验**：悬空引用、`$expect` 类型不匹配、`$id` 重复都会阻止发布
  （`PublishError.issues` 带路径）；草稿节点天然是不可达垃圾，即整体回滚。
- **可逆内联导出**：命名节点在首次遇到处完整内联（带 `$id`），之后所有
  出现（含回边）输出 `{ "$ref": id }`；只导出从指定根可达的子图（裁剪）。
- **GC**：对所有保留版本做标记-清除；旧快照钉住的节点不会被回收，
  `gc()` 返回实际回收的节点集合。

## 文件

- `src/model.ts` — JSON/节点/槽位类型
- `src/arena.ts` — 节点竞技场：身份、反向引用、浅/深克隆
- `src/loader.ts` — JSON 树 → 图（带路径的错误）
- `src/view.ts` — 只读视图：路径探测、引用状态、子树物化
- `src/exporter.ts` — 可逆内联导出（键排序、数组保序）
- `src/store.ts` — `GraphStore` / `Draft` / `Version`、发布校验、GC
- `test/graph-store.test.ts` — Node 内置测试（`node:test`）

## 实际运行结果

环境：Node.js v23.9.0（nvm），macOS。命令：
`node --experimental-strip-types --test 'test/*.test.ts'`

```
✔ two roots share an object; updating one branch copy leaves the other root untouched (1.92475ms)
✔ self-loop survives export and re-import with the same identity (0.269167ms)
✔ deleting a document leaves references pending, not null (0.530583ms)
✔ a failing update rolls back entirely; a fixed draft then publishes (0.248417ms)
✔ gc never reclaims nodes pinned by old snapshots (0.236125ms)
✔ round-trip preserves the identity-relation matrix and content, not just JSON text (0.547916ms)
✔ type-mismatched ref blocks publication; old version stays readable (0.136292ms)
✔ object key order is insignificant, array order is significant (0.249834ms)
ℹ tests 8
ℹ pass 8
ℹ fail 0
ℹ duration_ms 541.130542
```
