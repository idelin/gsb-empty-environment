即时通信与物联网控制信令网关在解析长连接 XML 流（RFC 6120 XMPP 风格）时，若未禁止 DTD/内部实体扩展（Billion Laughs 膨胀攻击）、允许在 `StartTLS` 握手完成前接受 `SASL` 认证报文，或在 `StartTLS` 升级后未清空旧明文缓冲残留的注入节（Stanza Smuggling），中间人可窃取明文凭据或向加密通道注入伪造控制指令。为 Go 协议网关宿主构建可导入的增量 XML 流安全解析与 StartTLS 状态机防御包，严禁依赖第三方 XML 流库。组件逐帧解析原始 UTF-8 字节流：硬拒绝任何 `<!DOCTYPE`/`<!ENTITY` 声明、注释炸弹与超过深度/属性数上限的畸形标签；严格执行状态机顺序 `StreamOpen` -> `StartTLS` -> `ResetBufferAndStream` -> `SASLAuth` -> `ResourceBind`，强制在 `StartTLS` 升级瞬间丢弃 TLS 握手前预读缓冲中的所有未处理字节（彻底阻断明文缓冲注入），并拒绝明文阶段发起 SASL 或业务节。附实体膨胀、StartTLS 剥离与明文缓冲走私攻击测试。

## `streamguard`

根包 `streamguard` 可直接导入当前 module：

```go
import sg "formal.local/w27-8ca5752422"

guard := sg.NewGuard(sg.DefaultConfig())
frames, err := guard.Feed(rawNetworkChunk)
```

核心组件：

- `Parser` / `FrameReader`：零第三方 XML 依赖的增量 UTF-8 tokenizer 与 XMPP 风格帧组装器。
- `Limits`：限制缓冲、嵌套深度、属性数、名称、文本、注释和 PI 长度。
- `Machine`：强制 `StreamOpen -> StartTLS -> ResetBufferAndStream -> SASLAuth -> ResourceBind`。
- `Guard`：组合解析器和状态机；遇到 `StartTLS` 后停止解析后续流水线字节。
- `Guard.UpgradeTLS()` / `Guard.ResetBufferAndStream()`：在 TLS 握手前清空全部未处理明文预读字节。
- `Guard.UpgradeServerConn` / `UpgradeClientConn`：可选的标准库 `crypto/tls` 握手集成。

典型服务端流程：

1. 将网络字节写入 `Guard.Feed` 或 `Guard.Write` + `ReadFrame`。
2. 收到并授权 `FrameStartTLS` 后发送 XMPP `<proceed/>`。
3. 调用 `UpgradeTLS` 清空旧明文缓冲，再执行 TLS 握手。
4. 将解密后的新字节继续送入 Guard，完成新 `StreamOpen`、`SASLAuth` 与 `ResourceBind`。

安全默认值会拒绝 DTD/ENTITY、自定义实体引用、注释炸弹、过深嵌套、过多属性、无效 UTF-8，以及 TLS 前的 SASL 和业务节。

运行全部攻击与状态机测试：

```sh
go test -v ./...
```
