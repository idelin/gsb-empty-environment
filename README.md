# 恶意Beaver三元组篡改秘密乘法

多方计算宿主用Beaver三元组做秘密乘法，离线提供者若给出c≠a*b会悄悄改结果。给Go测试宿主实现三方加法分享协议，有限域为调用方给定不超过65521的素数p；秘密x由三份标量和模p表示。三元组每项包含a、b、c的三方份额及唯一tripleID，参与方先用牺牲检查成对验证：对候选T和随机检查T'（两项tripleID不同且此前未使用），宿主以三方commit-reveal种子共同生成公开非零r，打开rho=a-r*a'、sigma=b-b'，再打开c-r*c'-rho*b'-r*sigma*a'-rho*sigma，结果须为0，否则两项都废弃；所有打开值由三方广播份额并用Ed25519签名绑定轮次、tripleID对和字段，重复发送者或不同值形成作弊证据。通过检查的T用于乘法：打开d=x-a、e=y-b，各方输出z_i=c_i+d*b_i+e*a_i，固定0号参与方再加d*e。每个tripleID只能消费一次，检查与消费状态持久原子；任一签名、范围或打开失败不产生z。实现诚实/恶意提供者夹具，在p≤17时穷举a,b,c,a',b',r核对牺牲检查接受条件，并测试篡改份额、广播分叉、重用triple和崩溃恢复；报告只能说明通过本题检查，不把单次随机检查称绝对正确。

## 实现概要

包 `netsec` 提供：

- `Field`：校验 `p <= 65521` 的素数域及加减乘、逆元、随机采样。
- `SplitSecret` / `Recombine`：三方标量加法分享。
- `Triple`、`TripleDigest`：三元组份额与 ID 绑定。
- `Host.Sacrifice`：带 Ed25519 签名广播和 commit-reveal 的成对牺牲检查。
- `Host.Multiply`：消费一个已检查三元组并输出三方 `z_i` 份额。
- `HonestProvider` / `MaliciousProvider`：诚实与恶意离线提供者夹具。
- `MemoryStore` / `FileStore`：持久化 triple 生命周期；文件存储使用同目录临时文件、`fsync`、原子 `rename` 和 sidecar `flock`。

最小用法：

```go
f, _ := netsec.NewField(17)
provider, _ := netsec.NewHonestProvider(17, nil)
candidate, _ := provider.Triple("T")
check, _ := provider.Triple("T-check")
h, _ := netsec.NewHost(17)

res, err := h.Sacrifice(candidate, check) // res.Accepted == true
_ = res
_ = err

xShares, _ := f.SplitSecret(3, nil)
yShares, _ := f.SplitSecret(4, nil)
mul, err := h.Multiply(candidate, xShares, yShares) // mul.Produced, mul.Z
_, _ = mul, err
```

状态语义：候选三元组检查通过后为 `checked`，消费后为 `consumed`；检查用三元组和检查失败/协议失败的候选三元组均为 `burned`。进程在 `checking` 或 `multiplying` 中途退出时，重新打开 `FileStore` 会分别保守恢复为 `burned` / `consumed`，因此不会恢复出可重用的 ID。

## 测试

```sh
go test ./...
go test -race ./...
```

完整范围和安全边界见 `REPORT.md`。
