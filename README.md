# QPACK 敏感字段索引隔离

HTTP/3 反向代理把多个租户的请求复用到同一上游连接时，动态表可能把“猜中的
Cookie 值”变成短索引，从而形成压缩长度侧信道。本包给 Go 代理提供保守的
QPACK 编码策略：`Cookie`、`Authorization` 和调用方配置的名称始终编码成带
QPACK `N` 位的字面量字段行；编码器不插入动态表，也不引用动态表项。下游解码
得到的字段若已经带 `N` 位，转发时继续保留。

## 使用

```go
encoder, err := netsec.NewEncoder(netsec.Config{
    SensitiveHeaders:         []string{"x-api-key", "x-tenant-token"},
    MaxDynamicTableCapacity: 4096,
})
if err != nil {
    return err
}

fields := encoder.Policy().Mark([]netsec.HeaderField{
    {Name: "cookie", Value: requestCookie},
    {Name: "x-trace-id", Value: traceID, Sensitive: true},
    {Name: "x-api-key", Value: apiKey},
})

block, err := encoder.Encode(fields)
```

`Encode` 返回完整 QPACK 字段段（Required Insert Count 和 Delta Base 均为 0）。
若代理已有自己的字段段前缀逻辑，可调用 `EncodeFieldLines` 只取得字段行。

## 安全性质

- 敏感字段只使用 `Literal Field Line with a Literal Name`，并设置 `N=1`。
- 所有字段都不进入动态表，因此不同租户之间不存在可被长度观测的值索引。
- 重复 Cookie 和空值保持原字段数及原值，不合并、不省略。
- 动态表容量为 0 或其他容量时行为一致；容量只在配置中记录，不改变输出。
- 编码不做 Huffman 压缩，猜中与猜错但等长的值得到完全相同的长度。

运行测试：

```sh
go test ./...
```
