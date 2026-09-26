# depgraph

从客户端/服务端 span 配对构建依赖边。纯 Go 标准库，遥测事件在本地合成、
可由夹具（fixture）回放；不接任何监控平台，也不是展示看板。

## 计数定义

1. 同一 `request_id` 的 client span 与 server span 属于同一次请求，
   在 `client.service -> server.service` 这条边上**只算一次调用**，去除双计。
2. 重试的每个 attempt 有独立 `request_id`，**各 attempt 各算一次调用**，
   各自携带自己的成功/错误结果，不合并、不去重。
3. 成功与错误**分开统计**；配对成功时以 server span 的状态为准。
4. 未配对的 span 不计入任何边，作为**不确定证据**保留
   （`client_without_server` / `server_without_client`）。
5. 缺服务名时记为 `unknown`，**绝不**从 operation 或对端猜测真实服务名。

## 结构

- `internal/telemetry` — span 模型与确定性本地合成器（`Synthesize(seed)`）
- `internal/pairing` — 配对与边聚合（上述全部计数规则）
- `internal/fixture` — JSON 夹具读写与按起始时间回放
- `cmd/depgraph` — CLI：合成夹具 / 回放夹具 / 直接合成分析
- `fixtures/sample.json` — 由 seed=42 合成的样例夹具

## 运行样例

```console
$ go run ./cmd/depgraph -synthesize fixtures/sample.json
synthesized 28 spans (seed 42) -> fixtures/sample.json

$ go run ./cmd/depgraph -fixture fixtures/sample.json
spans: 28
edges (from -> to, success, error):
  api -> store  success=3 error=1
  api -> unknown  success=1 error=0
  web -> api  success=7 error=1
uncertain evidence: 2
  [client_without_server] kind=client service="api" op="GET /query" request=id-000049 attempt=1 status=error
  [server_without_client] kind=server service="api" op="POST /callback" request=id-000052 attempt=1 status=ok
```

直接合成（`-seed 42`，默认）输出与夹具回放完全一致。

样例数据核对：web→api 为 6 次成功 + 一次重试（attempt1 错误、attempt2 成功），
共 success=7 error=1；api→store 为 3 成功 1 错误；api→unknown 是 server 端
缺服务名的一次成功调用；另有 2 条未配对 span 进入不确定证据。

## 测试结果

```console
$ go test ./...
ok  	depgraph/internal/fixture	# 3 个测试：合成确定性、夹具回放一致、回放有序
ok  	depgraph/internal/pairing	# 8 个测试：配对去双计、重试按 attempt、成败分开、
                                #           server 状态优先、两类未配对证据、缺名不猜、边排序
```

全部 11 个测试通过（Go 1.24，仅标准库）。
