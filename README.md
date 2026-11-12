# pickle字节流安全解码

数据服务收到旧版客户端的 pickle 字节流，攻击者用 GLOBAL、STACK_GLOBAL、REDUCE 或 BUILD 让原实现执行任意调用。为Go服务实现可导入的安全解码包：只支持协议 2 至 5 的纯数据操作码（PROTO、FRAME、MARK、STOP、NONE、BININT1、BININT2、BININT、BINUNICODE、SHORT_BINUNICODE、EMPTY_LIST、EMPTY_DICT、EMPTY_TUPLE、APPEND、APPENDS、SETITEM、SETITEMS、TUPLE、BINPUT、BINGET、MEMOIZE），其余操作码立刻拒绝；memo 引用必须已定义，深度不超过 32，栈与 memo 各不超过 1 万项，STOP 后有多余字节拒绝。用 REDUCE 载荷、未定义 memo、深度超限和合法嵌套结构验证。

## 使用

根包名为 `netsec`，可直接导入：

```go
import "example.org/netsec/q0541"

value, err := netsec.Decode(payload)
if err != nil {
    // 拒绝整个字节流；不会执行任何 import、GLOBAL、REDUCE 或 BUILD 行为
}
```

`netsec.SafeDecode` 是同一行为的显式别名。解析结果使用纯 Go 数据表示：

- `nil`、`int64`、`string`
- `[]any`（list）
- `map[any]any`（dict，键只接受 `nil`、`int64`、`string`）
- `netsec.Tuple`（tuple，便于与 list 区分）

## 安全边界

- 仅接受 README 中列出的操作码；其他任何操作码在首次出现时返回 `ErrOpcode`。
- `PROTO` 必须是首操作码，且版本只能是 2、3、4、5。
- `BINGET` 只能引用已定义的 memo 槽；`MEMOIZE`/`BINPUT` 只在栈顶取值，不弹栈。
- 数据嵌套深度最多 32 层；栈和 memo 各最多 10000 项。
- 帧声明长度与操作数不能越界，`STOP` 后不得存在额外字节。
- 拒绝循环引用、非法 UTF-8 字符串和不支持的复合字典键。

错误均可用 `errors.Is` 对应到 `ErrProtocol`、`ErrOpcode`、`ErrMemoUndefined`、`ErrDepthLimit`、`ErrStackLimit`、`ErrMemoLimit`、`ErrTrailingData` 等哨兵值。

## 验证

```sh
go test ./...
```
