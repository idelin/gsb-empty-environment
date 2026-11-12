# DNSSEC 信任锚撤销等待

离线设备维护信任锚（trust anchor）时，解析宿主可能暂时看见新钥，但新钥
不能立即替换旧钥。本包 `netsec` 实现供 Go 解析宿主调用的锚状态机。

## 状态机语义

- 锚钥为原始 Ed25519 公钥，`KeyID` 是公钥的 SHA-256 十六进制摘要。
- 生命周期：`unknown → candidate → trusted → revoked`。
- 新钥首次被观察到即成为候选（candidate）；必须在每个观察 tick 中都
  出现、且持续时间达到配置的 `ObservationPeriod`，才提升为受信锚。
- 候选钥在任一 tick 缺失，观察窗口立即清零；再次出现时重新计时。
- 受信锚不会因缺失而过期，新旧两把锚并存，直到被显式撤销。
- 撤销标志（`RevocationNotice`）必须由当前受信锚对
  `RevocationMessage(target)` 的 Ed25519 签名验证通过；候选钥无权撤销。
- 撤销后写入墓碑（tombstone），该钥永远无法重新进入候选或受信集合。

## 可信时间与持久化

- 时间来自可注入的 `Clock` 接口（默认 `SystemClock`），状态中持久化
  `last_time` 高水位；任何回拨（含重启后回拨）都返回 `ErrClockRollback`，
  状态机 fail-closed，不推进任何计时。
- 每次成功 tick 都推进并原子持久化高水位：状态 JSON 先写同目录临时文件、
  `fsync` 后 `rename`，崩溃不会留下半写文件。
- 候选的 `observed_since`、受信集合、撤销墓碑全部落盘，重启后观察进度
  不丢失、不被时钟回拨利用。

## 宿主调用

```go
m, err := netsec.Open(netsec.Config{
    Path:              "/var/lib/resolver/anchors.json",
    Clock:             netsec.SystemClock{},
    ObservationPeriod: 30 * 24 * time.Hour,
    BootstrapKeys:     [][]byte{initialAnchor},
})
if err != nil { /* ... */ }

// 每个观察周期调用一次，present 为本轮看到的全部锚公钥。
if err := m.Observe(visiblePublicKeys); err != nil { /* ... */ }

// 收到受信锚签发的撤销标志时调用。
if err := m.Revoke(notice); err != nil { /* ... */ }

for _, id := range m.Trusted() { /* 仅信任这些锚 */ }
```

## 测试

`machine_test.go` 使用可任意推进/回拨的假时钟 `fakeClock`，覆盖：

- 重启后观察窗口延续（`TestRestartPreservesObservationWindow`）。
- 进程内与重启后的时间回拨被拒绝（`TestClockRollbackRejected`、
  `TestRollbackBlocksPromotionAfterRestart`）。
- 短暂出现后缺失导致观察重置（`TestBriefAppearanceResetsObservation`、
  `TestMissingTickResetPersistsAcrossRestart`）。
- 两把锚并存且旧锚不过期（`TestTwoAnchorsCoexist`）。
- 撤销签名校验、候选无权撤销、撤销墓碑防重入。

运行：`go test -race ./...`
