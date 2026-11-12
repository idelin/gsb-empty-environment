# BSON 长度与键一致性校验

`example.org/netsec/q0213` 提供无第三方依赖的 BSON 校验器，供网关请求处理器在把原始 BSON 交给数据库之前调用。解析器按声明长度严格行走，不依赖数据库驱动的宽松解析行为。

## 校验规则

- 顶层文档声明的 4 字节长度必须等于 `len(data)`，尾部多余字节或截断都拒绝。
- 嵌套文档和数组必须完整落在父文档声明的边界内，不能吞掉父文档终止符。
- 元素名 cstring 必须在文档终止符之前以 `0x00` 结尾。
- 字符串长度必须包含结尾 `0x00`，且声明范围的最后一个字节确实是 `0x00`。
- 同一文档层内键名逐字节比较，重复键拒绝；不同层可以同名。
- 零值 `Policy` 是严格策略，拒绝以 `$` 开头的键和含 `.` 的键；调用方可按字段放行。
- 默认嵌套上限 100 层，可用 `Policy.MaxDepth` 调整。

## 请求处理器用法

```go
import netsec "example.org/netsec/q0213"

func validateRequestBody(body []byte) error {
    // 严格策略：拒绝重复键、$ 前缀键和含点键。
    return netsec.Validate(body)
}

func validateUpdateBody(body []byte) error {
    // 仅在业务确实需要操作符/点路径时显式放行。
    return netsec.ValidateWithPolicy(body, netsec.Policy{
        AllowDollarKeys: true,
        AllowDottedKeys: true,
        MaxDepth:        32,
    })
}
```

失败时返回 `*netsec.ValidationError`，其中包含绝对字节偏移和键路径，例如 `profile.tenant`。可用 `errors.Is` 分类：

```go
err := netsec.Validate(body)
switch {
case errors.Is(err, netsec.ErrTotalLength):
    // 总长度与缓冲区不一致
case errors.Is(err, netsec.ErrDuplicateKey):
    // 同层重复键
case errors.Is(err, netsec.ErrNestedOverflow):
    // 嵌套文档越过父边界
}
```

## 测试

`go test ./...` 覆盖合法文档、全部 BSON 标量类型、总长度长短错位、嵌套长度越过父边界、未终止 cstring、字符串长度漏算结尾 NUL、重复 `tenant` 键、数组重复索引、`$`/`.` 策略以及随机输入不崩溃。
