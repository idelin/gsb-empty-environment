# P-256 可验证盲求值

这个包实现了 P-256 上的隐私查询盲求值与 DLEQ 换绑保护。服务端返回的
`E=kB` 和证明同时绑定可信公钥 `K`、盲点 `B`、宿主上下文和本次求值；服务端
不会接受请求自报的公钥。

## 使用

`HashToGroup` 和随机源都由可信宿主注入。`HashToGroup` 负责把查询输入映射为
规范、非无穷远的 P-256 点；如果宿主上下文也需要参与映射，可使用
`NewClientWithContext`。

```go
privateKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
server, _ := netsec.NewServer(privateKey, rand.Reader)
client, _ := netsec.NewClient(hostHashToGroup, rand.Reader, server.Key())

request, state, err := client.Blind(input, hostContext)
if err != nil {
    return err
}
response, err := server.Evaluate(request, hostContext)
if err != nil {
    return err
}
digest, err := state.Complete(response) // 状态在这里被消费
```

`state.Complete` 在成功或失败时都会消费一次性状态。重复调用返回
`ErrStateConsumed`。随机源失败时 `Blind` 返回 `nil` 请求和 `nil` 状态，不会
产生可发送的 `B`。

## 转录编码

- DLEQ 挑战为 `SHA256(domain, context, K, B, E, A, D) mod q`。
- 每个字段前加 4 字节大端长度；点使用 33 字节规范压缩 SEC1 编码。
- 证明满足 `s=t-c*k mod q`，验证方重建 `A=sG+cK` 和 `D=sB+cE` 后重算 `c`。
- `c`、`s` 必须非负且严格小于 `q`。
- 最终摘要为
  `SHA256(completionLabel || context || input || compressed(Z))`，其中
  `Z=r^-1E`。

宿主提供的 `HashToGroup` 质量和随机源质量属于包外信任边界；本包不会回退到
`math/rand` 或其他随机源。

运行测试：

```sh
go test -race ./...
```
