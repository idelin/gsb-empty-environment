# graphser

Go（仅标准库）的对象图序列化：支持共享引用与循环，分配稳定对象 ID，
解码时先建占位对象再连接引用，并区分「值相等」与「对象相同」。

## 支持的值域

- `nil`、`bool`、`string`
- `float64`（仅有限值）；`int` / `int64` / `uint64` 在 float64 精确范围内会被接受并归一化为 `float64`
- `[]any` —— 纯值，无身份；内容相等即可互换
- `*graphser.Object` —— 有身份的堆对象；身份即 Go 指针相等

其他类型（结构体、map、channel、函数等）一律被 `Marshal` 拒绝。

## 线格式

纯数据 JSON 文档，不含类型名，解码不执行任何宿主代码：

```json
{"version":1,"count":1,"objects":[{"id":1,"fields":{"name":"x","self":{"ref":1}}}],"root":{"list":[{"ref":1},{"ref":1}]}}
```

- 节点 = 标量 | `{"list":[...]}` | `{"ref":id}`
- ID 按首次遇到顺序分配（字段按字典序遍历），同一图序列化结果逐字节确定
- `count` 必须与 `objects` 记录数一致；ID 必须唯一；`ref` 必须指向已声明对象（悬空引用拒绝）

## 解码流程

1. 校验：输入大小、版本、count、ID 唯一性、引用目标、长度/深度上限——全部在分配任何图节点之前完成
2. 为每条 object 记录分配占位 `*Object`
3. 连接字段与引用

因此两个指向同一对象的引用解码后仍是同一指针，自环与多节点环均保留。

## 值相等 vs 对象相同

- 对象相同：`*Object` 指针相等（`==`）
- 值相等：`graphser.ValueEqual(a, b)` 做结构化比较，忽略身份，可处理循环

## 限制（解码端强制）

`MaxInputBytes` 32MiB、`MaxObjects` 2^20、`MaxListLen` 2^20、
`MaxStringLen` 8MiB、`MaxFields` 2^16、`MaxDepth` 512。

## 运行

```sh
go test ./...
go run ./cmd/demo
```
