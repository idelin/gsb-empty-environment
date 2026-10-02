# merkleanchor — 带时间戳与防重放签名的默克尔锚定器

外部存证锚定组件（纯 Go 标准库）。定时将本地系统的默克尔根哈希与当前时间戳打包签名；
支持出示带时间戳证明的存证证书，证明特定数据在特定历史时间点之前已经确定存在。

## 组件

- `merkle.go` — 默克尔树：域分离哈希（叶子 `0x00` / 节点 `0x01`）、根计算、包含证明生成与校验。
- `anchor.go` — 锚定器 `Anchorer`：对 `(ChainID, Seq, Timestamp, Root, Nonce)` 做 ECDSA P-256 签名；
  验证器 `Verifier`：校验签名、拒绝未来时间戳，并以单调序号 + 随机数登记表防重放。
- `certificate.go` — 存证证书：默克尔包含证明 + 时间戳锚定签名。
  校验通过即证明数据在 `Anchor.Timestamp` 之前已入库固定，无法事后伪造。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
[t=1000] 锚定完成: seq=1 root=7c795ce56d70c3da...
         签名=dd4ff2b76b56ac217b9b634a3f82eca9...
[t=2000] 已为 "tx-002: carol pays dave 3" 签发存证证书（默克尔路径 2 步）
[t=2000] 证书校验通过：该业务事实必然在 t=1000 之前已入库固定
[攻击] 事后伪造数据签发证书被拒绝: certificate: data not covered by this anchor
[攻击] 重放同一张证书被拒绝: anchor: replay detected (seq or nonce already seen)
```

## 测试

```sh
go test ./... -v
```

覆盖：默克尔证明往返与篡改检测、锚定签名与时间戳校验、重放/乱序/跨链拒绝，
以及验收场景（t=1000 锚定 → t=2000 出示凭据 → 证明数据在 t=1000 前已固定）。
