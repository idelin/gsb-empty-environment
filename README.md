# HTTP2禁用连接头

HTTP/2里带上transfer-encoding或connection后，降级到HTTP/1就会变成走私。伪头之外禁止connection、transfer-encoding、keep-alive、upgrade；出现即整流失败。普通自定义头保留。协议网关调用可导入的Go库做这次裁决。在合法请求上分别加这四个头。

## 裁决规则

- 常规头（非 `:` 伪头）中出现 `connection`、`keep-alive`、`transfer-encoding`、`upgrade` 任一时，返回流级错误，网关应回 RST_STREAM（PROTOCOL_ERROR），不得转发。
- 头名按 HTTP/2 规则小写后大小写不敏感匹配；伪头（如 `:method`）豁免。
- 合法请求原样通过，不删除、不改写任何字段，普通自定义头保留。
- 附带校验 HTTP/2 字段顺序：常规头之后再出现伪头同样整流失败。

## 用法

```go
import "example.org/netsec/q0038"

// 网关解码 HEADERS 帧后得到有序字段列表
fields := []netsec.HeaderField{
    {Name: ":method", Value: "POST"},
    {Name: ":path", Value: "/gateway"},
    {Name: "x-custom-header", Value: "keep-me"},
}

if err := netsec.AdjudicateRequest(fields); err != nil {
    streamErr := netsec.AsStreamError(err) // *StreamError -> RST_STREAM PROTOCOL_ERROR
    rstStream(streamErr)
    return
}
// 无错误：含自定义头在内的字段原样转发
```

使用标准库 `http.Header` 的入口提供 `AdjudicateHTTPHeader`；`IsForbiddenHeader` 可单独查询某个头名是否违禁。

## 测试

```sh
go test ./...
```

测试在同一组合法请求（伪头 + `content-type` + 自定义头）上分别追加四个违禁头，断言全部整流失败，并覆盖大小写不敏感、自定义头保留、字段不被改写和伪头乱序。
