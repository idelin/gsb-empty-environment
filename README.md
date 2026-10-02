# jsonproj — 结构化 JSON 扁平化列式投影器

纯 Go 标准库实现。把半结构化 JSON 文档流按字段路径拆解为独立虚拟列；
查询只指定少数叶子路径时，仅物化相关列，无关子树只做词法跳过，
不反序列化整个 JSON 大串。

## 设计

- `scan.go`：基于 `json.Decoder` 的流式扫描器。
  - `selector` 按目标路径集合判定每个路径的行为：`actCapture`（物化）、
    `actDescend`（下钻）、`actSkip`（跳过）。
  - `skipValue` 只推进解码器游标、统计 token，不构建任何 `map`/`slice`，
    这是“不反序列化无关结构”的关键。
  - 数组元素路径统一记为 `父路径[]`，如 `items[].price`。
- `store.go`：列存 `Store`，`map[path][]value`，每行一篇文档；
  缺失路径补 `nil`，新出现路径向前回填 `nil`，保证列等长。
- `project.go`：两个入口
  - `Flatten(io.Reader)`：全量模式，自动推断所有字段路径并建列。
  - `Project(io.Reader, paths)`：投影模式，只抽取指定列，返回 `Stats`
    （物化值数 / 跳过 token 数 / 被跳过的子树）用于验证裁剪行为。
- 数字用 `json.Number` 保留字面量，避免大整数精度损失。

## 验收场景

输入复杂嵌套订单 JSON（含 `user.address`、`items` 商品大列表、`payment` 等）。
投影器为 `user.address.city` 建立独立列存切片；提取城市维度时，
`items`（每单 1000 个商品）等无关子树仅被词法跳过：

```
== Project(user.address.city) ==
  row0 -> Beijing
  ...
== Scan stats (只读相关列的证据) ==
  docs processed : 5
  values captured: 5      (只有城市列被物化)
  tokens skipped : 65100  (items 等子树仅词法跳过，未反序列化)
```

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 运行样例
```
