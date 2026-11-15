# Ruby Marshal流类型门禁

服务若对不可信输入调用 `Marshal.load`，攻击者能借用户定义类与 `marshal_load`、`_load` 钩子触发 gadget。本包为 Go 入口网关提供一个纯字节级的 Marshal 4.8 门禁，请求处理器在把流交给任何反序列化逻辑之前先调用它即可。

## 用法

```go
gate := netsec.NewGate([]string{"Time", "Date"})

if err := gate.Validate(requestBody); err != nil {
    // 返回 400，并且绝不把 requestBody 交给 Marshal.load。
}
```

需要自定义预算时：

```go
gate := netsec.NewGate(
    []string{"Time"},
    netsec.Limits{MaxDepth: 32, MaxElements: 4096},
)
```

一次性校验也可以使用 `netsec.Validate(data, allowedClasses)` 或 `netsec.ValidateWithLimits(...)`。默认预算为 `DefaultMaxDepth = 100`、`DefaultMaxElements = 100000`。

## 校验规则

- 头部必须严格为 `0x04 0x08`，根值之后不得有尾随字节。
- 允许 `0`、`T`、`F`、`i`、`"`、`:`、`;`、`[`、`{`、`I`、`@`。
- `o` 仅在其类名（包括经 `;` 符号链接解析后的类名）位于调用方白名单时允许。
- `u`、`U`、`e`、`C`、`d`、`S`、`M` 以及其余未列出的类型码一律拒绝。
- `;` 和 `@` 只能引用已经出现的符号/对象索引；自引用容器是合法后向链接。
- 超过嵌套深度或元素总数预算的流会被拒绝；声明的容器尺寸会在循环前预检。
- 解析器只读取字节、维护计数和符号位置，不会实例化 Ruby 对象，也不依赖 Ruby 运行时。

错误可用 `errors.Is` 与 `ErrInvalidHeader`、`ErrTruncated`、`ErrForbiddenType`、`ErrClassNotAllowed`、`ErrInvalidReference`、`ErrDepthLimit`、`ErrElementLimit`、`ErrInvalidFormat` 等哨兵值匹配。
