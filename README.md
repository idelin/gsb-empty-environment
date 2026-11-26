TLS 1.3（RFC 8446）密钥派生时间表在遭遇 `HelloRetryRequest`（HRR）时有一条极易遗漏的转录改写规则：当服务端回发 HRR 后，转录哈希必须把原先的 `ClientHello1` 替换成合成握手消息 `message_hash = 0xFE || 0x00 0x00 || HashLen || Hash(ClientHello1)` 再拼接 HRR 与 `ClientHello2`，直接续拼原始 `ClientHello1` 会导致后续所有握手密钥和 `Finished` MAC 全部对不上；此外双向 `KeyUpdate` 只能更新单一方向密钥并独立归零该方向 64 位记录序号。为 TLS 协议栈交付可导入的 Go RFC 8446 密钥时间表与记录加解密状态机（支持 `TLS_AES_128_GCM_SHA256` 与 `TLS_AES_256_GCM_SHA384`）。组件实现精确的 `HKDF-Expand-Label`（2 字节长度 + `'tls13 '` 前缀标签 + 上下文长度与内容）、`Derive-Secret`、三级密钥链（Early Secret 含可选 PSK 绑定器 `binder_key` -> Handshake Secret 含 ECDHE 共享密钥 -> Master Secret 含导出密钥 `Exporter` 与 `resumption_master_secret`）、`Finished` 校验值计算，以及完整记录保护层（`nonce = iv XOR zero_pad64(seq_num)`、TLS 1.3 内部内容类型尾字节加任意长度零填充剥离、序号触及 `2^64 - 1` 拒绝加密防 nonce 重用、以及 `KeyUpdate` 单向平滑切换密钥并归零序号）。验收直接对照 RFC 8448 标准测试向量（普通 1-RTT 握手、含 HRR 重试握手、PSK 0-RTT/恢复握手与 KeyUpdate 切换）逐字节核对每阶段 Secret、Key、IV 与 `verify_data`，并覆盖双向独立 KeyUpdate、非法零填充/篡改密文拒绝与序号溢出拦截。

## 实现说明

根目录包名为 `tls13`，仅依赖 Go 标准库，支持：

- `TLS_AES_128_GCM_SHA256` 与 `TLS_AES_256_GCM_SHA384`
- 精确的 `HKDF-Expand-Label`、`Derive-Secret` 与 ACVP/RFC 8448 常量核对
- Early / Handshake / Master 三级密钥链、PSK `binder_key`、Exporter、`resumption_master_secret`
- Finished/Binder HMAC
- AES-GCM 记录保护、TLS 1.3 内部内容类型和零填充处理
- nonce XOR 64 位序号、序号耗尽拦截
- 收/发方向独立 Rekey 与序号归零
- RFC 8446 HRR 的 `message_hash` 转录替换

导入：

```go
import tls13 "prescreen.local/ps-ede6cb136b"
```

密钥时间表：

```go
early := tls13.NewEarlySecret(tls13.TLS_AES_128_GCM_SHA256, psk)
handshake := early.HandshakeSecret(ecdheSharedSecret)
master := handshake.MasterSecret()

clientHS, _ := handshake.ClientHandshakeTrafficSecret(transcriptHash)
serverHS, _ := handshake.ServerHandshakeTrafficSecret(transcriptHash)
finished := tls13.CalculateVerifyData(suite, serverHS, transcriptHash)
```

HRR 后不要直接续拼 `ClientHello1`：

```go
tr, _ := tls13.NewHRRTranscript(suite, clientHello1, helloRetryRequest)
_ = tr.WriteMessages(clientHello2)
transcriptHash := tr.Sum()
```

记录层与 KeyUpdate：

```go
records, _ := tls13.NewRecordProtector(suite, clientAppSecret, serverAppSecret, tls13.RoleClient)
wire, _ := records.Seal(tls13.ContentTypeApplicationData, payload, nil)
typ, plaintext, err := records.Open(wire)

keyUpdate, _ := records.InitiateKeyUpdate(tls13.KeyUpdateRequested)
// 先用旧 outbound key seal/send keyUpdate，再提交：
_ = records.CommitOutboundKeyUpdate()
```

测试：

```bash
go test -race ./...
```

`rfc8448_simple_test.go` 与 `acvp_test.go` 逐字节固定已确认的 RFC/NIST 密钥向量；`record_test.go`、`hrr_psk_test.go` 覆盖 HRR 改写、PSK Binder、双向 KeyUpdate、篡改、非法填充和序号溢出。
