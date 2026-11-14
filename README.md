# QUIC CID / Stateless Reset Token Lifecycle

本模块为 QUIC 栈提供边缘网关侧的连接 ID（CID）与 stateless reset token
生命周期管理。核心目标是：**绝不让旧连接的 reset token 变成新连接可接受的
令牌**，即使攻击者完整观察过旧连接流量。

## 安全不变量

- 每个 token 都以 `HKDF-SHA256(generationKey, generation || route || cid)` 派生，
  完整 CID 与路由分区都参与绑定。
- CID 状态机固定为 `issued -> active -> retiring -> retired/closed -> expired`；
  `RETIRE_CONNECTION_ID` 的重复、乱序、未知序号都是幂等/忽略事件，不会关闭连接。
- retired/closed CID 的 token 保留到 `retire/close 时间 + MaxPacketLifetime +
  ConfirmationGrace` 之后。该窗口是保守的串行窗口，保证最大包寿命和确认宽限
  都已经结束。
- 本地 retire 后若 peer 确认帧丢失，从 retire 请求时间起同样受该窗口约束；
  到期会进入 `expired` 并释放密钥引用，不会永久悬挂。
- 一个 CID 值一旦签发就进入 `CIDLedger` 墓碑集合，隔离期结束甚至 token 过期后
  也不会再签发。网关内所有连接共享同一个 ledger。
- 每个派生出的 token 也进入 `TokenLedger` 永久墓碑集合；任何历史 token 值
  都不会再绑定到新 CID，避免旧令牌重放命中新连接。
- stateless reset 检测顺序严格为：最小 21 字节 -> 固定比特 -> 短首部 ->
  已知短首部解密探测 -> 尾部 16 字节常量时间比较。探测失败或令牌碰撞都 fail
  closed，不关闭任何连接。
- token 只在其所属连接的管理器中匹配；网关跨连接检测要求唯一 owner。多个
  owner 或同一连接内多个 CID 共享 token 时记为歧义事件，不触发关闭。
- 密钥代次销毁前会重新扫描活跃与隔离 CID，并检查 `KeyRing` 引用计数；任一
  引用存在时拒绝擦除密钥。
- 审计记录是带序号、SHA-256 前向哈希链和恢复密钥 HMAC 的 witness。持久化
  journal 拒绝重复/回退序号；恢复需要 MAC 签名的 checkpoint，并逐条验证
  witness HMAC，旧日志前缀、分叉或被篡改记录都会被拒绝。

## 单连接用法

```go
clock := netsec.NewManualClock(time.Now()) // 生产环境使用 netsec.RealClock{}
gen := netsec.SequentialCIDGenerator([]byte{0x01}, 8)
journal := netsec.NewMemoryJournal()

m, err := netsec.NewManager(netsec.Config{
    Clock:             clock,
    Generator:         gen,
    Route:             "edge-a",
    MaxPacketLifetime: 30 * time.Second,
    ConfirmationGrace: 3 * time.Second,
    MasterSecret:      persistedMaster,
    RecoveryKey:       persistedRecoveryKey,
    Journal:           journal,
    Label:             "conn-1",
})

grant, err := m.IssueCID()       // 返回 CID、sequence、token、审计 witness
_, err = m.ConfirmPeerUse(grant.CID)
_, err = m.RequestRetire(grant.CID, "retire_prior_to")
ret, err := m.RetireFromPeer(grant.Sequence) // 永不关闭连接

d := m.DetectReset(packet, netsec.ResetOptions{
    Route: "edge-a",
    Probe: func(p []byte) bool {
        return quicCanDecryptShortHeader(p) // true 表示是合法密文包
    },
})
if d.Closed {
    // d.Witness、d.Close.Witnesses 是导致状态转换的审计见证
}
```

如果 QUIC 栈在调用前已经完成短首部解析和认证失败，可省略 probe 并设置
`CallerVerifiedUndecryptable: true`，或调用 `DetectResetOnly`。未做该断言又没有
probe 时会返回 `ResetNoProbe`，不关闭连接。

## 网关用法

```go
g, err := netsec.NewGateway(netsec.GatewayConfig{
    Clock:             clock,
    Generator:         gen,       // 所有连接共享
    MasterSecret:      master,
    RecoveryKey:       recoveryKey,
    MaxPacketLifetime: 30 * time.Second,
    ConfirmationGrace: 3 * time.Second,
})
a, _ := g.OpenNamedConnection("conn-a", "edge-a")
b, _ := g.OpenNamedConnection("conn-b", "edge-a")

// 旧连接的 token 重放只会命中 conn-a；conn-b 不受影响。
d := g.DetectReset(oldPacket, netsec.ResetOptions{Route: "edge-a", Probe: probe})
```

`Gateway` 共享 CID generator、`CIDLedger` 与 `TokenLedger`，因此即使不同连接
调用同一生成器，重复 CID 也会被 `ErrCIDReused` 拒绝。
生产网关应设置 `JournalFactory` 为每个连接生成独立 `FileJournal`，并按连接
保存对应的签名 checkpoint，再调用 `Manager.Recover`。

## 密钥轮换与销毁

```go
rot, _ := m.RotateKeys()          // 新 CID 使用新代次；旧 CID/token 保持绑定
m.PurgeExpired()                  // 推进到期状态并释放引用（也可由检测/销毁自动触发）
status := m.KeyStatus(rot.OldGeneration)
if status.Destroyable {
    _, err = m.DestroyKeyGeneration(rot.OldGeneration)
}
```

当前代次不可销毁。旧代次必须等到所有引用 CID 进入 `expired` 后，
`KeyStatus(...).Destroyable` 才为真。

## 崩溃恢复与防回滚

`Manager.Checkpoint()` 返回带 HMAC 的高水位（序号 + 链头摘要）。
`Manager.Recover(journal, checkpoint)` 在全新的 manager 上执行：

1. 验证 checkpoint MAC；
2. 验证完整审计哈希链与每条记录 HMAC；
3. 拒绝 checkpoint 序号高于日志、日志在 checkpoint 处分叉、或日志尾部被截断；
4. 按顺序重建 CID、token、密钥引用、连接关闭状态和 CID 墓碑；
5. 若停机期间已过保留期限，则追加 expiry witness 并释放密钥引用。

`FileJournal` 每写一条记录即 `fsync`，打开时丢弃崩溃留下的非完整尾行。
生产环境必须把 `MasterSecret`、`RecoveryKey` 与 checkpoint 分开、单调地持久化；
如果攻击者同时回滚日志和全部外部锚点，任何纯软件方案都无法检测。

## 测试覆盖

运行：

```sh
go test ./...
go test -race ./...
```

测试包含可控时钟、确定性/注入式 CID 生成器、丢包与重传、旧 token 重放、
跨连接令牌碰撞、跨路由分区、乱序 `RETIRE_CONNECTION_ID`、NAT 重绑定、
合法 stateless reset、密钥引用销毁、文件日志重开，以及崩溃回滚/截断恢复。
