# 区块链 JSON-RPC 方法门控

供 Go 反向代理在转发前调用的 fail-closed JSON-RPC 2.0 门控包。

## 使用

```go
gate, err := netsec.New(netsec.Config{
	AllowedMethods:      netsec.DefaultAllowedMethods,
	MaxBatchSize:        50,
	MaxGetLogsBlockSpan: 5_000,
})
if err != nil {
	log.Fatal(err)
}

proxyHandler := gate.Middleware(upstreamProxy)
```

只想在自定义请求入口校验时调用 `gate.Validate(body)`；需要读取并恢复
`http.Request.Body` 时调用 `gate.ValidateRequest(r)`。

## 校验规则

- 仅接受 JSON-RPC 2.0 请求对象或非空批量数组，版本必须精确为 `2.0`。
- 批量中任意元素的方法不在白名单，整批拒绝，绝不部分转发。
- 批量元素数超过 `MaxBatchSize` 时拒绝。
- JSON 对象任意层级出现重复键即拒绝，包括转义后相同的键。
- `jsonrpc`、`method`、`params`、`id` 的大小写变体键拒绝；方法名按字节精确匹配。
- `eth_getLogs` 的 `toBlock - fromBlock` 不得超过 `MaxGetLogsBlockSpan`。
  无法计算上界的标签组合（例如数字起始块配 `latest`）按 fail-closed 拒绝；
  两端都省略或使用同一个标签时允许。
- 请求体默认上限 1 MiB，可配置。

默认白名单只含常用只读方法，不包含 `personal_*`、`debug_*` 或管理方法。
