# hellopeek

四层网关要按SNI把TLS连接转给不同后端，但不终止TLS。给Go网关实现ClientHello窥探包：从连接读取并缓存原始字节，处理握手消息跨多个TLS记录、记录逐字节到达的情况，拒绝重复扩展、长度不一致和中间插入的非握手记录；解析出SNI与ALPN后，把已读字节原样交还给转发连接。枚举所有切分位置验证结果一致，并确认后端收到的字节流与客户端发出的相同。

## 实现

- `PeekClientHello(conn net.Conn) (*Info, *Conn, error)` 读取并缓存 ClientHello
  所需的完整线路字节，解析出 SNI 与 ALPN，返回的 `*Conn` 先逐字节回放缓存前缀，
  再透明读取底层连接，TLS 会话本身不会被终止。
- 记录层用 `io.ReadFull` 装配，天然支持握手消息跨多条 TLS 记录以及记录头/载荷
  逐字节到达。
- 校验项：重复扩展、各类长度不一致（记录、握手头、extensions、SNI/ALPN 列表）、
  ClientHello 完成前插入的非握手记录、错误的记录版本与超限记录。

## 测试

- `TestPeek*`：手工构造 ClientHello，枚举每个切分位置（单切点、逐字节、随机多切分），
  断言解析结果一致且回放字节流与客户端输入完全相同。
- `TestPeekRejectsMalformed`：覆盖重复扩展、长度不一致、插入非握手记录等拒绝路径。
- `TestEndToEndTLSThroughPeeker`：基于 `crypto/tls` 的真实握手，验证网关窥探后透明转发，
  后端独立完成握手并协商 SNI/ALPN，且后端收到的字节流与客户端发出的一致。

运行：`go test -race ./...`
