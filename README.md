# A/B 固件更新防回滚状态机

`netsec` 是一个可导入的 Go 固件宿主状态机。它把唯一可信的防回滚依据放在
宿主提供的 `RollbackFuse` 中；普通状态快照可以被崩溃截断、丢失或被攻击者
替换成任意旧版本，但恢复和启动决策始终以熔丝为准。

## 安全不变量

- 熔丝只保存单调不减的 32 位 `rollbackIndex`，本包从不降低熔丝。
- 安装只写普通状态中的 `Active.Other()` 槽。镜像完整写入并重读 SHA-256
  摘要后，才用一次状态写入标记为候选。
- 候选首次启动前，先持久化尝试次数，再把熔丝提升到候选索引并读回确认。
  提升、读回或校验失败都不会返回启动会话。
- 运行镜像必须回报本次启动随机数、槽位和镜像摘要。确认成功后，成功标记
  与 active 槽在同一个状态 blob 中原子提交。
- 每次 `BeginBoot` 都作废旧随机数；随机数只保存在内存中，重启后不可重放。
- 候选尝试耗尽后可回退到另一槽，但候选和回退槽的索引都不得低于熔丝。
- 恢复先删除低于熔丝的候选，再选择已验签、已成功且不低于熔丝的最高版本；
  没有可用版本时返回恢复模式，不会降低熔丝。
- 同索引不同摘要默认拒绝；策略在创建宿主时固定。

## 宿主接口

```go
type RollbackFuse interface {
	ReadRollbackIndex() (uint32, error)
	RaiseRollbackIndex(to uint32) error
}

type StateStore interface {
	Load() ([]byte, error) // 未写入时返回 ErrNoState
	Save([]byte) error     // 必须原子替换
}

type SlotImages interface {
	WriteImage(slot SlotID, image []byte) error
	ReadImage(slot SlotID) ([]byte, error)
}
```

`StateStore.Save` 只能原子替换普通状态 blob。实现可以丢失旧写、保留新写，
也可以被攻击者恢复成旧 blob；不应把它与熔丝放在同一信任域。

验签回调为：

```go
type ManifestVerifier func(manifest Manifest, signature, image []byte) error
```

回调必须验证签名确实覆盖清单且绑定镜像。除此之外，状态机会独立检查设备
型号、槽位无关摘要、`rollbackIndex` 和 `MinBootVersion`。
`Config.CurrentBootVersion` 表示当前引导器版本，小于清单最低引导版本时安装
失败。

## 使用流程

```go
host, err := netsec.New(netsec.Config{
	DeviceModel:        "board-1",
	CurrentBootVersion: 3,
	MaxAttempts:        3, // 0 使用默认值 3
	// AllowEqualIndexDifferentDigest: false,
	VerifyManifest: verify,
}, fuse, store, images)

err = host.Install(netsec.InstallRequest{
	Image:     image,
	Manifest:  manifest,
	Signature: signature,
})

session, err := host.BeginBoot()
if err != nil {
	// ErrFuseRaise/ErrFuseVerify/ErrRecoveryMode 等均表示禁止启动。
}
// 将 session.Image 和 session.Nonce 交给本次启动的镜像。

err = host.ConfirmBoot(netsec.BootConfirmation{
	Nonce:       session.Nonce,
	Slot:        session.Slot,
	ImageDigest: session.ImageDigest,
})

result, err := host.Recover()
```

`NewMemoryFuse`、`NewMemoryStore` 和 `NewMemoryImages` 是参考实现。`MemoryStore`
保留每次 `Save` 的快照，`RestoreSnapshot`/`ResetToNoState` 可用于在测试中模拟
攻击者回滚。

## 状态内容

普通 `State` 包含 `Active` 和两个 `SlotState`。每槽保存：

- `ImageDigest`：实际镜像的规范 SHA-256 摘要；
- `Manifest` 与 `ManifestSignature`：签名清单；
- `VersionIndex`：与清单 `RollbackIndex` 一致的版本索引；
- `Candidate`、`Success`：候选和成功标记；
- `Attempts`：尚未确认的启动尝试次数。

## 测试

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖安装写镜像、回读摘要、候选提交、熔丝提升及读回、尝试计数、确认与
active 原子切换的所有状态写故障点（提交前和提交后），并枚举每个历史普通
状态快照，验证任何允许启动的镜像索引都不低于熔丝。
