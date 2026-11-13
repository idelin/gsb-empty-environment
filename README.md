# 暂挂后移除

CRL里先暂挂再出现removeFromCRL，证书仍被当成永久吊销。removeFromCRL只解除同一序列的暂挂；没有对应暂挂则该条目无效。只有暂挂而没有移除时按暂挂处理。可导入的Go状态机接证书路径送来的状态。对从未暂挂的序列放入移除条目。

## 状态机

`netsec` 包实现按证书序列号跟踪 CRL 吊销状态的状态机，供证书路径验证层导入使用。

状态（`State`）：

- `Good`：未吊销（从未出现过的序列号也按此处理）
- `Held`：暂挂（`certificateHold`，临时吊销）
- `Revoked`：永久吊销，不可逆

迁移规则（见 `Transition`）：

- `certificateHold`：`Good/未知 -> Held`；`Held -> Held`；`Revoked` 保持 `Revoked`
- `removeFromCRL`：仅当当前为 `Held` 时回到 `Good`；对从未暂挂或已永久吊销的
  序列号，该条目无效（返回 `ErrInvalidRemoveFromCRL`，状态不变）
- 其他任何原因（含未携带原因的 `Unspecified`）：进入 `Revoked`

用法：

```go
m := netsec.NewMachine()
m.Apply(netsec.Entry{SerialNumber: serial, Reason: netsec.ReasonCertificateHold})
// m.State(serial) == netsec.Held
m.Apply(netsec.Entry{SerialNumber: serial, Reason: netsec.ReasonRemoveFromCRL})
// m.State(serial) == netsec.Good

// 对从未暂挂的序列号放入移除条目：无效，状态保持 Good
if _, err := m.Apply(netsec.Entry{SerialNumber: other, Reason: netsec.ReasonRemoveFromCRL});
    errors.Is(err, netsec.ErrInvalidRemoveFromCRL) {
    // 按规则忽略该条目
}

// 直接喂给标准库解析出的 CRL
rl, _ := x509.ParseRevocationList(der)
m.ApplyRevocationList(rl)
```
