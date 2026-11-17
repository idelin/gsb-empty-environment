# 离线信任锚轮换状态机

设备维护离线信任锚时，暂时看见的新钥不会立即替换旧钥。新钥必须连续出现在宿主提供的完整键快照中，满足 `Config.ObservationPeriod` 后才会晋升；任一次快照缺失都会重置观察窗口。旧锚不会因为“消失”而被隐式删除，只有通过受信锚签名验证的撤销操作才会移除，并留下不可重新加入的墓碑。

## 宿主调用

```go
store := netsec.NewFileStore("/var/lib/device/trust-anchors.json")
m, err := netsec.Open(netsec.Config{
	ObservationPeriod: 30 * 24 * time.Hour,
	Clock:             netsec.NewSystemClock(),
	Store:             store,
})
if err != nil {
	return err
}
if !m.Initialized() {
	if err := m.Bootstrap(initialTrustedAnchors); err != nil {
		return err
	}
}

// currentAnchors 是本次从离线介质解析出的“全部”在册公钥，不是增量。
result, err := m.Reconcile(currentAnchors)

// 宿主解析到撤销标志后，使用原受信钥对规范载荷签名。
payload := netsec.RevocationPayload(target.KeyID())
signature := ed25519.Sign(originalTrustedPrivateKey, payload)
revoked, err := m.Revoke(target.KeyID(), signature)
```

`Reconcile` 必须每次传入完整集合。只传新增键会把未传入的候选键视为缺失并重置其观察期。`Bootstrap` 只用于全新存储；已有状态通过 `Open` 恢复。

## 时间与持久化

- `Clock.Now()` 返回可信单调纳秒。`SystemClock` 在进程内使用 `time.Time` 的单调分量；`ManualClock` 供宿主测试或已有可信 tick 源使用。
- 每次状态变更都会把候选键的 `Since`、最近可信 tick、活动锚和撤销墓碑原子写入 `Store`。
- 加载后若时钟读数低于持久化高水位，状态机把有效时间钳制在高水位，不会缩短观察期，并在 `ReconcileResult.ClockRegressed` 中报告。
- 观察期作为持久化配置的一部分校验；重启时不能静默缩短观察期。
- `FileStore` 使用同目录临时文件、`fsync` 和原子 `rename`，文件权限为 `0600`。

## 撤销约束

撤销签名使用 `RevocationPayload(targetID)`。签名只能由当前活动锚验证；候选键、未受信键和任意第三方签名都会被拒绝。最后一个活动锚不可直接撤销，避免系统进入无根的自动学习状态。

## 测试

`go test ./...` 使用假时钟覆盖：完整观察期与两锚并存、候选键短暂缺失后重置、跨重启保留截止时间、重启后时钟回拨、撤销签名来源、墓碑持久化，以及文件存储权限。
