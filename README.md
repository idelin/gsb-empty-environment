# 区块链JSON-RPC方法门控

攻击者用批量数组、重复method键和大小写变体，想让对外的区块链JSON-RPC端口触达personal_unlockAccount或debug_*。给Go反向代理写供请求入口调用的方法门控包：按JSON-RPC 2.0解析，批量中任一元素不在白名单则整批拒绝，重复键拒绝，方法名精确匹配；eth_getLogs的区块跨度、批量条数有上限。用混入危险方法的批量、重复键和超跨度验证。

## methodgate 包

`methodgate` 是无状态的请求入口守卫，挂在反向代理上游 handler 之前：

```go
gate, err := methodgate.New(methodgate.Config{
	AllowedMethods: methodgate.DefaultAllowedMethods(), // 不含 personal_*/debug_*/admin_*
	MaxBatchSize:   100,    // 单批元素上限，0 用默认值
	MaxGetLogsSpan: 10000,  // eth_getLogs 的 toBlock-fromBlock 上限
})
if err != nil {
	log.Fatal(err)
}

// proxy 是现有的 *httputil.ReverseProxy
http.ListenAndServe(":8545", gate.Handler(proxy))
```

也可以在自定义入口直接调用 `gate.Inspect(body []byte) error`，返回值始终是
`*methodgate.RPCError`，可直接序列化为 JSON-RPC 2.0 错误响应。

### 门控规则

- 严格 JSON-RPC 2.0：单请求为对象、批量为数组，校验 `jsonrpc == "2.0"` 与 `method`。
- 重复键：基于 token 扫描，对象内任意键重复即按 `-32700` 拒绝（`encoding/json`
  默认保留最后一个值，会被用来夹带危险方法）。
- 精确匹配：方法名区分大小写、不做前缀/小写归一，`ETH_CHAINID`、`eth_chainIdX`
  这类变体会被 `-32601` 拒绝。
- 批量全有或全无：任一元素不在白名单，整批拒绝并标注出错元素下标。
- 批量条数：超过 `MaxBatchSize` 按 `-32000` 拒绝；空数组按 `-32600` 拒绝。
- `eth_getLogs`：`fromBlock`/`toBlock` 均为数字时校验跨度，超限按 `-32000` 拒绝；
  `latest`/`pending` 等命名标签无确定跨度，交由上游节点处理。

### 验证

```sh
go test -race -cover ./...
```

测试覆盖混入 `personal_unlockAccount`/`debug_*` 的批量、重复 `method` 键、
大小写/前缀变体、超跨度 `eth_getLogs` 以及超大批量等攻击向量。
