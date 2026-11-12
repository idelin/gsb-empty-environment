# 区块链JSON-RPC方法门控

攻击者用批量数组、重复method键和大小写变体，想让对外的区块链JSON-RPC端口触达personal_unlockAccount或debug_*。给Go反向代理写供请求入口调用的方法门控包：按JSON-RPC 2.0解析，批量中任一元素不在白名单则整批拒绝，重复键拒绝，方法名精确匹配；eth_getLogs的区块跨度、批量条数有上限。用混入危险方法的批量、重复键和超跨度验证。

## 用法

请求入口在向上游节点转发前调用 `Gate.Inspect`，返回非 nil 即拒绝整批/单个请求：

```go
gate := netsec.NewGate(netsec.Config{
    AllowedMethods: []string{"eth_blockNumber", "eth_getLogs", "net_version"},
    MaxBatchSize:   100,   // 0 使用默认值 100
    MaxBlockRange:  10000, // 0 使用默认值 10000
})
if err := gate.Inspect(requestBody); err != nil {
    // 拒绝并记录原因，错误可用 errors.Is 匹配哨兵错误
}
```

## 门控规则

- 严格按 JSON-RPC 2.0 解析：根节点必须是对象或数组，`jsonrpc` 必须为 `"2.0"`，未知字段和尾随数据拒绝。
- 方法名与白名单做精确、大小写敏感匹配，`ETH_GETLOGS`、`Eth_GetLogs` 等变体一律拒绝；未列入白名单的 `personal_*`、`debug_*` 返回 `ErrMethodNotAllowed`。
- 批量数组逐条检查，任一元素不过则整批拒绝；条数超过 `MaxBatchSize` 返回 `ErrBatchTooLarge`。
- 全文档（含嵌套对象和批量元素）用 tokenizer 检测重复键，返回 `ErrDuplicateKey`，防止重复 `method` 键用后值覆盖前值绕过结构体检。
- `eth_getLogs` 的 `fromBlock`/`toBlock` 为数值（hex quantity 或裸数字）时校验 `toBlock - fromBlock <= MaxBlockRange`；区块标签（`latest` 等）和 `blockHash` 形式不做静态跨度检查。

## 测试

```sh
go test ./...        # 覆盖危险方法混入批量、重复键、大小写变体、超跨度、超限批量等场景
go test -race ./...
```
