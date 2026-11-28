# WireGuard 会话生命周期状态机

这是一个无外部依赖、可导入的 Go 包，实现 WireGuard `Noise_IKpsk2` 会话层：

- X25519 静态/临时密钥交换
- BLAKE2s-256 / keyed BLAKE2s-128 / HMAC-BLAKE2s KDF
- ChaCha20-Poly1305 数据面，XChaCha20-Poly1305 Cookie Reply
- `Current` / `Previous` / `Next` 三槽密钥轮转
- 响应方延迟晋升（首条有效传输报文确认后才轮转）
- TAI64N 严格单调时间戳防重放
- `underLoad` 时 MAC1 -> Cookie Reply -> MAC2 两步握手
- 120 秒软重密钥、180 秒硬销毁
- 每会话独立的 64 位发送计数器与 2048 位接收重放窗口

模块内包名为 `wireguard`，导入路径为 `prescreen.local/ps-394659603d`。

## 快速使用

两个对端各自持有一个 `Peer`：

```go
import (
    "net/netip"
    wg "prescreen.local/ps-394659603d"
)

aliceKeys, _ := wg.GenerateKeyPair()
bobKeys, _ := wg.GenerateKeyPair()

alice, _ := wg.NewPeer(wg.Config{
    LocalPrivateKey: aliceKeys.Private,
    RemotePublicKey: bobKeys.Public,
})
bob, _ := wg.NewPeer(wg.Config{
    LocalPrivateKey: bobKeys.Private,
    RemotePublicKey: aliceKeys.Public,
})

const (
    aliceAddr = "192.0.2.10:51820"
    bobAddr   = "192.0.2.20:51820"
)
bobEP := netip.MustParseAddrPort(bobAddr)
aliceEP := netip.MustParseAddrPort(aliceAddr)

// 启动方发起握手。
initiation, _ := alice.Initiate()

// 响应方校验 Initiation，派生新密钥放入 Next，返回 Response。
result, _ := bob.HandlePacket(initiation, aliceEP)
response := result.Reply // result.Action == wg.ActionHandshakeResponse

// 启动方收到 Response 后立即晋升 Current。
result, _ = alice.HandlePacket(response, bobEP)
// result.Action == wg.ActionSessionEstablished

// 启动方首包（也可以是 nil，即 0 字节 Keepalive）。
packet, _ := alice.Encrypt(nil)

// 响应方收到首条用新会话索引加密的有效传输报文后，才原子执行
// Previous = Current; Current = Next; Next = nil。
result, _ = bob.HandlePacket(packet, aliceEP)
// result.Confirmed == true
```

正常数据面：

```go
send, err := alice.Send([]byte("hello"))
if err != nil { /* ... */ }

result, err := bob.HandlePacket(send.Packet, aliceEP)
if err != nil { /* ... */ }
plaintext := result.Payload

if send.RekeyNeeded {
    // 旧键仍可继续收发；调用 Initiate 启动新握手。
    initiation, _ := alice.Initiate()
    _ = initiation
}
```

## 三槽状态机

| 事件 | 密钥槽变化 |
| --- | --- |
| 响应方发出 `Handshake Response` | `Next = new`，`Current` 不变 |
| 响应方尚未确认时尝试 `Send` | 若无旧 `Current` 返回 `ErrHandshakeUnconfirmed`，绝不使用 `Next` 发包 |
| 启动方收到合法 `Handshake Response` | `Previous = Current; Current = new`，可立即发包 |
| 响应方收到 `Next` 索引的首条有效传输报文（含 Keepalive） | `Previous = Current; Current = Next; Next = nil` |
| 三槽中已有旧键 | `Previous` 只用于接收在途乱序包，不用于发送 |
| `Previous`/`Next` 超过硬界限 | 对应槽被清零并移除 |

所有槽操作都在 `Peer.mu` 下完成，确认与轮转是原子的。

## 会话硬界限

| 界限 | 默认值 | 行为 |
| --- | --- | --- |
| `RekeyAfterMessages` | `1 << 60` | 触发新握手建议，旧键继续工作 |
| `RekeyAfterTime` | `120s` | 触发新握手建议，旧键继续工作 |
| `RejectAfterMessages` | `2^64 - 2^13 - 1` | 销毁该会话，拒绝一切收发 |
| `RejectAfterTime` | `180s` | 销毁该会话，拒绝一切收发 |

`Send` 返回的 `SendResult.RekeyNeeded` 以及 `NeedsRekey()` / `NeedsHandshake()`
用来驱动主动重密钥。寿命达到硬界限后，`Send`、`HandlePacket` 返回
`ErrSessionExpired`，对应密钥先被清零；过期密钥不会继续用于解密。

`Expire()` 可用于定时器主动清理；所有收发路径也会惰性执行同样的检查。
测试或特殊部署可通过 `Config.Limits` 覆盖界限，默认值严格对应上述常量。

## Cookie 限载

```go
bob.SetUnderLoad(true)

initiation, _ := alice.Initiate()
result, _ := bob.HandlePacket(initiation, aliceEP)
// MAC1 合法但缺少有效 MAC2：
// result.Action == wg.ActionCookieReply
// len(result.Reply) == 64

// 启动方解密并保存 Cookie。
result, _ = alice.HandlePacket(result.Reply, bobEP)
// result.Action == wg.ActionCookieStored

// 下一次 Initiate 自动填充 MAC2；响应方验过 MAC2 才执行 DH。
retry, _ := alice.Initiate()
result, _ = bob.HandlePacket(retry, aliceEP)
// result.Action == wg.ActionHandshakeResponse
```

实现细节：

- MAC1 在所有握手包上先校验；无效 MAC1 直接丢弃，不返回 Cookie Reply。
- `underLoad == true` 时，缺少/错误 MAC2 不会执行昂贵 DH，只返回 Cookie Reply。
- Cookie = `keyed-BLAKE2s-128(cookie_secret, source_ip || source_port)`。
- Cookie Reply 使用 XChaCha20-Poly1305，24 字节随机 nonce，关联数据为触发消息的 MAC1。
- `cookie_secret` 每 120 秒轮换；轮换期保留上一把秘密键，允许刚好在途的旧 Cookie。
- 启动方处于高负载时同样会对 `Handshake Response` 发起 Cookie 挑战；响应方保留
  已发送 Response 的状态，收到 Cookie Reply 后填充 MAC2 重发。

## 防重放

- Handshake Initiation 携带 12 字节 TAI64N；生成器保证即使系统时钟停走或回拨，
  每次产生的时间戳也严格递增。
- 每个对端维护历史最大时间戳；`<=` 最大值的 Initiation 返回 `ErrReplay`。
- 每个会话有独立的 2048 位接收位图窗口。窗口内乱序可接收，重复或过旧计数器
  返回 `ErrReplayWindow`。
- 传输计数器为 64 位；nonce 为 4 字节零 + 8 字节小端计数器。

## 主要 API

```go
func NewPeer(Config) (*Peer, error)

func (p *Peer) Initiate() ([]byte, error)
func (p *Peer) HandlePacket(pkt []byte, source netip.AddrPort) (*Result, error)
func (p *Peer) Send(payload []byte) (*SendResult, error)
func (p *Peer) Encrypt(payload []byte) ([]byte, error)
func (p *Peer) Decrypt(pkt []byte) ([]byte, error)

func (p *Peer) SetUnderLoad(bool)
func (p *Peer) NeedsRekey() bool
func (p *Peer) NeedsHandshake() bool
func (p *Peer) Expire()
func (p *Peer) Sessions() SessionSnapshot
func (p *Peer) Close()
```

`HandlePacket` 的 `Action` 表示需要发送 `Reply`、已保存 Cookie、已建立会话，
或返回了解密后的 `Payload`。

## 测试

```bash
go test ./...
go test -race ./...
```

测试覆盖：

- BLAKE2s、keyed BLAKE2s、HMAC-BLAKE2s、Poly1305、ChaCha20-Poly1305、
  HChaCha20/XChaCha20-Poly1305 标准向量
- 响应方未收确认首包前不晋升 `Next`
- 首条 0 字节 Keepalive 触发三槽原子轮转
- `Previous` 接收旧键在途乱序包
- TAI64N 重放拦截与严格单调生成
- 高负载 MAC1 / Cookie Reply / MAC2 两步握手（双向）
- Cookie 秘密键轮换
- 120 秒软重密钥与 180 秒硬销毁
- 64 位发送计数界限、2048 位接收窗口、篡改报文拒绝
