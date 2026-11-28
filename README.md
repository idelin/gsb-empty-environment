高吞吐 VPN 网关在实现 IPsec ESP（RFC 4303）64 位扩展序列号（ESN）与加密认证解密时，若先验证解密后的填充字节再校验 ICV 认证标签（触发 Padding Oracle 攻击）、在 32 位低序列号回绕时错误估算高 32 位导致合法大流量断流或重放窗口失效，或在未通过认证前就推进滑动窗口右边界，攻击者可发送伪造高序列号包瞬间将合法通信窗口推飞实现永久 DoS，或利用重放与填充预言机恢复明文。为 Go 网络安全宿主实现可导入的 RFC 4303 ESP（含 64 位 ESN）封装/解封与抗重放状态机包，严禁联网（仅用标准库 `crypto/aes`/`crypto/cipher`/`crypto/hmac`/`crypto/sha256`）。组件严格实现：（1）支持可配置窗口大小（64/256/1024 位位图）的 RFC 4303 Appendix A.2 64 位 ESN 完整滑动窗口算法：精确区分低 32 位位于窗口左/右及跨 2^32 回绕边界时的 `seq_h` 候选估算，仅在密码学认证（AEAD AES-GCM 或 Encrypt-then-MAC HMAC-SHA256）恒定时间验真**之后**才更新最高已见 64 位序列号与位图（彻底阻断未认证包推飞窗口 DoS）；（2）严格禁止序列号 0 与 64 位溢出回绕（到达 `2^64-1` 强制要求 Rekey 硬拒收）；（3）解封时先验 ICV/GCM Tag，再在恒定结构内校验末尾 `Pad Length`、`Next Header` 以及每字节递增填充序列 `1, 2, ..., PadLen`（拒绝错乱填充与超长 `PadLen`），绝不向调用方区分返回“认证失败”与“填充错误”以防预言机泄漏。附跨 2^32 回绕测试、未认证高序列号推飞窗口阻断、重放拦截与错误填充测试。

## 实现

根目录提供可直接导入的 `esp` 包（模块路径为本仓库的 `go.mod`）。仅使用 Go 标准库，无外部依赖。

主要 API：

- `NewReplayWindow(64|256|1024)` / `(*ReplayWindow).Check` / `(*ReplayWindow).Commit`
- `NewAESGCM(key, windowSize...)` 与 `NewEncryptThenMAC(aesKey, macKey, windowSize...)`
- `(*Session).Seal` / `SealNext` / `Open` / `Decapsulate`

`Check` 只读取窗口并基于最高已见 64 位 ESN 重建候选 `seq_h`。只有 `Open` 完成 ICV/GCM Tag 验真和 ESP trailer 校验后，才会调用 `Commit` 推进最高序列号与位图。伪造的高序号包认证失败时不会移动窗口。

### 报文布局

所有模式共享四字节 SPI 与四字节低 32 位序列号。完整 64 位 ESN 不传输，但以 `high32 || SPI || low32` 作为认证数据参与 GCM AAD / HMAC-SHA256 输入。

- AES-GCM：`SPI | SeqLow | 12 字节 nonce | ciphertext | 16 字节 tag`
- Encrypt-then-MAC：`SPI | SeqLow | 16 字节 IV | ciphertext | 32 字节 HMAC-SHA256`

加密部分包含明文载荷、递增填充字节 `1..PadLen`、`Pad Length` 和 `Next Header`。解封时认证与填充错误统一返回 `ErrAuthentication`，不暴露具体失败阶段。序号 0、`2^64-1` 以及溢出回绕均拒绝，其中序列号空间耗尽返回 `ErrRekeyRequired`。

### 示例

```go
package main

import (
	"fmt"

	esp "formal.local/w28-28ad084317"
)

func main() {
	key := []byte("0123456789abcdef0123456789abcdef")
	sa, err := esp.NewAESGCM(key, 1024)
	if err != nil {
		panic(err)
	}
	packet, err := sa.Seal(1, []byte("hello"), 59) // Next Header 59 = IPv6 no-next-header
	if err != nil {
		panic(err)
	}
	payload, nextHeader, seq, err := sa.Open(packet)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%q nextHeader=%d seq=%d\n", payload, nextHeader, seq)
}
```

运行验证：

```sh
go test ./...
go test -race ./...
```
