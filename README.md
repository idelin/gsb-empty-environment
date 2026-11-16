# CoAP分块传输的重组预算与ETag一致

攻击者用 Block1 只发编号很大的块来占内存，或在传输中途换内容。为Go CoAP 服务端写可导入的 Block1 重组包：块大小 SZX 取 16 到 1024 字节且整个传输中不变；块必须按序，NUM 跳号拒绝并回 4.08；声明的总大小（Size1）超过 64 KiB 直接回 4.13；同一对端同时最多 4 个未完成传输；ETag 或 Content-Format 中途变化则丢弃整个传输。用大跳号、超限 Size1、第 5 个传输和中途换 ETag 验证。

## 用法

`block1` 包与传输层解耦：服务端把每个 Block1 请求转换为 `block1.Message`，
调用 `Handle`，再把返回的 `Result.Code` 映射回 CoAP 响应码。

```go
r := block1.New()
res := r.Handle(block1.Message{
    Peer: "client-addr", Resource: "/upload",
    Num: 0, More: true, SZX: 6, // 1024 字节块
    Size1: 4096, ETag: etag, ContentFormat: 0,
    Payload: payload,
})
// res.Code: 2.31 继续 / 2.04 完成(含 res.Payload) / 4.00 4.08 4.13 4.29
```

防护规则：

- SZX 仅允许 0..6（16–1024 字节），且整个传输期间不得改变，否则 4.00。
- 块必须严格按序到达；NUM 跳号（含超大跳号）回 4.08，不分配额外内存。
- Size1 超过 64 KiB 在分配任何状态前直接回 4.13；未声明 Size1 时累计超过
  64 KiB 同样回 4.13 并丢弃传输。
- 同一对端最多 4 个未完成传输，超出回 4.29。
- ETag 或 Content-Format 中途变化即丢弃整个传输并回 4.08。

运行测试：`go test ./block1/`
