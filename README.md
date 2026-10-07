# configmerge

按层顺序合并部署配置的 Go 库，并保留每个叶值的来源与被覆盖链。

## 合并语义

- **对象**（`map[string]interface{}`）：递归合并。
- **数组与标量**：整体替换先前值。
- **删除**（`merge.Delete` 哨兵值）：移除键及其整个子树；重复删除是幂等 no-op。
- **空值**（`nil`）：保留键，值为显式 null。

## 内部模型

合并按路径展开进行：每层先扁平化为 `路径 -> 叶值`，后层按键覆盖前层；
叶值会丢弃其下的子树，也会把祖先叶值替换为对象。结果再还原为嵌套 map。

## API

- `merge.Merge(layers...) *Result` — 按序合并，`Result.Merged` 为最终配置。
- `Result.Leaf(path)` / `Result.Leaves()` — 最终叶值、来源层及被覆盖链（`History`）。
- `Result.Removals(path)` — 该路径的被移除档案（删除/父级替换）。
- `Result.Explain(path)` — 解释任意路径：存在则给出来源与覆盖链；
  不存在则区分 `ReasonNeverSet`、`ReasonDeleted`、`ReasonDroppedByParent`、
  `ReasonParentIsLeaf`，并给出触发的层与祖先路径。

路径为点分形式，如 `server.tls.enabled`。

## 测试

```sh
go test ./merge/
```

覆盖：递归合并、数组整体替换、删除与空值、类型切换（对象↔标量/数组）、
重复删除、删除后重设、父对象被替换后的路径解释、多层覆盖链。
