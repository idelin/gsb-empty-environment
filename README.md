# Protobuf未知字段保留策略

攻击者在未知字段里塞后端才认识的内部字段号，经网关原样转发给较新的后端。给Go网关写供解码路径调用的未知字段过滤包：调用方给已知字段与内部保留号段，落入保留段、group线类型、字段号0、超出调用方字节上限的未知字段一律拒绝，其余按原顺序保留以兼容新旧版本。用内部号段、嵌套长度和正常新字段验证。

## 用法

`netsec.Filter` 只依赖标准库，在解码路径上对整条序列化消息做一次线格式扫描：

```go
filter, err := netsec.NewFilter(
    map[int]struct{}{1: {}, 2: {}},        // 网关已知字段
    []netsec.Range{{Min: 1000, Max: 1999}}, // 后端内部保留号段
    4096,                                   // 单个未知字段的字节上限，0 表示不限
)
if err != nil {
    return err
}

safe, err := filter.FilterBytes(raw)
if err != nil {
    // 命中保留段 / group / 字段号 0 / 超限 / 线格式损坏，拒绝转发
    return err
}
// safe 中已知字段原样透传，合法未知字段保持原顺序，可继续转发
```

拒绝原因通过哨兵错误暴露，可用 `errors.Is` 匹配：`ErrReservedField`、
`ErrGroupField`、`ErrFieldNumberZero`、`ErrFieldTooLarge`、`ErrMalformed`、
`ErrInvalidConfig`。长度前缀字段只校验外层 framing（声明长度不得越过缓冲区
末尾），载荷按不透明字节透传，因此嵌套消息、字符串和 packed 重复字段都不受
影响。
