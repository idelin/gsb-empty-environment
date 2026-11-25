# 认证器步号迁移组件

`migration` 包根据旧设备签名迁移段和历史签名回执，计算新设备上：

- `Consumed`：在所有相容历史中都确定已消费的步号；
- `Unknown`：至少一个相容历史会消费，但无法确定具体步号；
- `Blocked`：未撤销迁移段发生新步号互斥冲突，或证据无效导致不能声明安全；
- `Safe`：不属于以上任一类的步号。

所有结果都是闭区间分解，内部用半开 `math/big` 区间表示，支持完整 `uint64` 域，不会按步号展开。

## 基本用法

```go
signer := migration.NewMemorySigner()
_ = signer.GenerateKey("old-device")
_ = signer.GenerateKey("new-device")

segment := migration.Segment{
    ID: "seg-1",
    OldDeviceID: "old-device",
    NewDeviceID: "new-device",
    Old: migration.Interval{Start: 100, End: 199},
    New: migration.Interval{Start: 400, End: 499},
    Orientation: migration.Forward,
    Signature: migration.Signature{KeyID: "old-device"},
}
segment, _ = signer.SignSegment(segment)

receipt := migration.Receipt{
    RequestID: "request-1",
    Side: migration.OldSide,
    DeviceID: "old-device",
    Steps: migration.Interval{Start: 120, End: 129},
    Signature: migration.Signature{KeyID: "old-device"},
}
receipt, _ = signer.SignReceipt(receipt)

cert, err := migration.BuildCertificate(migration.Input{
    NewDeviceID: "new-device",
    Domain: migration.Interval{Start: 0, End: 999},
    Segments: []migration.Segment{segment},
    Receipts: []migration.Receipt{receipt},
}, signer)
if err != nil || !cert.Acceptable() {
    // 证据不足或互斥冲突未解决，不能把 cert.Safe 当作可登录步号。
}

if ok, findings := migration.VerifyCertificate(cert, signer); !ok {
    // 独立验收器从 cert.Input 重算签名、映射、请求约束和区间分解。
    _ = findings
}
```

## 相容历史规则

- 同一 `RequestID` 的多个回执只形成一个请求消费，重试不会重复占用步号。
- 旧侧回执必须能由未撤销且签名有效的迁移段解释；平移段使用 `forward`，翻转段使用 `reverse`。
- 同一新步号被多个未撤销段覆盖时，重叠部分进入 `Blocked`，非重叠部分仍保留为可用映射。
- 新侧回执与旧侧回执同时存在时，取两侧解释后的交集。
- 每个请求必须选择一个互不相同的新步号；组件用区间单元上的容量网络检查所有相容分配。
- `Consumed` 是移除该步号容量后不存在完整相容分配的步号；`Unknown` 是仍存在可避开分配的可能消费步号。

证书内嵌原始 `Input`，并包含可重算的映射片段、请求解释和最终区间分解。验收器不直接信任这些派生字段。
