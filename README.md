# 离线信任时间单调性

设备断网时没有可信墙钟，更新元数据过期判断不能因用户调时而被绕过。实现供Go更新宿主调用的时间信任组件，输入签名时间证明及本机单调计时器，维护可信时间区间；重启后只保留已持久化下界，区间跨过截止点时返回不确定并阻止安装。证明须绑定设备挑战，拒绝重放。用假时钟和故障存储验证回拨、单调计时器重置及落盘失败不会扩展有效期。

## 时间信任组件 `timetrust`

包 `timetrust` 在 `timetrust/` 下，仅依赖标准库。

### 威胁模型与不变量

- 设备墙钟可被用户任意前拨/回拨，因此信任决策**只使用单调计时器**，墙钟读数从不参与区间计算。
- 唯一可信输入是服务器对**设备一次性挑战**（128 位随机 nonce）签名的 `TimeProof`（Ed25519，见 `SignProof` / `Ed25519Verifier`）。nonce 用后即焚，重放或外来 nonce 一律返回 `ErrReplay`。
- 接受证明后以签名时间为锚点，按单调计时增量和可配置漂移率（默认 100 ppm）维护区间 `[lower, upper]`；下界向下取整、上界向上取整，误差始终保守。
- 只有下界持久化（`LowerBoundStore`，`FileStore` 采用临时文件 + `rename` 原子替换）。重启后 `hasUpper=false`，只能证明“现在不早于 T”。
- 单调计时器回退（重启/计数器回绕）会丢弃上界，新的证明才能重建区间。
- 落盘先于内存锚点生效：`Save` 失败时不安装锚点、不产生上界，并向上返回错误，因此存储故障不可能延长有效期。
- 区间跨过或触及截止点返回 `DecisionUncertain`，宿主必须阻止安装；只有整个区间严格早于截止点才是 `DecisionValid`。

### 宿主集成

```go
store := timetrust.NewFileStore("/var/lib/update-host/timetrust")
trust, err := timetrust.New(timetrust.SystemClock{},
    timetrust.Ed25519Verifier{PublicKey: serverPublicKey}, store)

// 1. 向服务器发起带挑战的时间请求
nonce, err := trust.IssueChallenge()
// 2. 服务器返回签名证明后
if err := trust.ObserveProof(proof); err != nil { /* 阻断，重试 */ }
// 3. 判定更新元数据是否仍在有效期内
if trust.Evaluate(metadata.NotAfter) != timetrust.DecisionValid {
    // 阻止安装
}
```

### 验证方式

`go test ./...` 使用 `FakeClock`（独立控制墙钟与单调钟、可回拨/重置）和
`faultyStore`（可注入 `Save` 故障并记录每次尝试），覆盖：

- 墙钟回拨不改变任何信任输出与持久化下界；
- 单调计时器重置后上界消失、安装被阻断，新证明可恢复；
- 重启后仅恢复持久化下界，无证明前一切安装返回 `DecisionUncertain`；
- 重放、伪造签名、篡改时间戳、挑战过期、陈旧证明均被拒绝；
- 落盘失败不提交任何新状态，既有提交不被覆盖，恢复后经新证明或 `Checkpoint` 才能继续推进。
