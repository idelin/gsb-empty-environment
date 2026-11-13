# 签名 nonce 仿射关联的私钥暴露审计

`netsec` 是一个无第三方依赖、可直接导入 Go 密码审计宿主的 P-256 ECDSA
审计包。它检查两份签名是否与假设 `k2 = a*k1 + b (mod n)` 一致，并且只在
全部输入通过范围校验、两份原始签名都验签成功之后才做代数分析。

## 使用

```go
report, err := netsec.Analyze(netsec.Input{
    PublicKey:  pub,                       // *ecdsa.PublicKey, 必须是 P-256
    Digest1:    digest1,                   // 原始消息摘要；按 ECDSA 规则左侧截断
    Digest2:    digest2,
    Signature1: netsec.Signature{R: r1, S: s1},
    Signature2: netsec.Signature{R: r2, S: s2},
    A:          a,                         // 0 <= a < n
    B:          b,                         // 0 <= b < n
})
if err != nil {
    // 输入非法、公钥非法、标量越界，或任一份原始签名验签失败。
}
```

`Analyze` 从不修改输入。`VerifyInput` 可单独执行同一套强制预检。

## 结论语义

| `Report.Status` | 含义 |
| --- | --- |
| `recovered` | 至少一个符号解释得到通过全部检查的候选；`Candidates` 包含全部有效候选。 |
| `insufficient_information` | 线性方程出现 `0*d = 0` 的退化分支，现有证据不能确定私钥和 nonce；没有搜索私钥空间。 |
| `no_candidate` | 两份签名都验签成功，但四种符号选择均不符合给定仿射关系；**这不表示 nonce 安全**。 |

输入错误和验签失败通过 `error` 返回，可用 `errors.Is` 检查
`ErrScalarOutOfRange`、`ErrP256Required`、`ErrPointAtInfinity`、
`ErrPointNotOnCurve`、`ErrSignatureVerification` 等哨兵错误。

## 推导与独立重验

每份签名都检查 `s` 与 `n-s` 两个符号，共四种组合。记选定后的标量为
`s1`、`s2`，曲线阶为 `n`，则使用：

```text
s1*k1 = z1 + r1*d (mod n)
s2*k2 = z2 + r2*d (mod n)
k2 = a*k1 + b (mod n)

d = (a*s2*z1 - s1*z2 + b*s1*s2) * (s1*r2 - a*s2*r1)^(-1) (mod n)
```

`Report.Attempts` 保留四种符号选择的方程、`S1`、`S2`、行列式和右端项，
以及 `candidate`、`inconsistent`、`degenerate`、`candidate_rejected` 结果。
每个 `Candidate` 还带有生成它的 `Derivation` 快照，包括符号选择和方程。

候选只有在以下检查全部通过后才会返回：

- `d*G` 与输入公钥逐坐标相等；
- `k1*G` 与 `k2*G` 的 x 坐标分别等于 `r1`、`r2`；
- 两个 `s*k = z+r*d (mod n)` 方程及仿射关系成立。

`RecheckCandidate` 会重新验证输入并从原始数据独立重算上述条件，不信任报告中
的中间值；`Report.Recheck` 对所有候选重放，并在通过后调用宿主的独立回调：

```go
err := report.Recheck(input, func(c netsec.Candidate) error {
    // 仅在密码学校验通过后执行宿主自己的策略或记录。
    return nil
})
```

由于公钥固定了 `d`，多个有效候选通常表示 nonce 的符号解释不唯一。所有候选
都会返回，且 `Report.Ambiguous`、`DistinctPrivateKeys` 和说明性 `Notes`
会标明歧义。实现不会以相同 `r` 作为唯一检测条件，也不会枚举私钥空间。

## 测试

所有测试密钥和 nonce 都由包内本地确定性夹具创建，不连接签名服务，也不使用
网络。覆盖场景包括：

- 相同 nonce；
- 非零仿射偏移；
- 仅第二份签名做 low-S 规范化；
- 无仿射关系但两份签名均验签成功；
- 相同 `r` 但不是指定关系；
- 退化线性方程；
- 48 字节摘要的 ECDSA 左侧截断；
- 所有标量边界、非 P-256 曲线、无穷远点、曲线外点及验签失败；
- 候选独立重验、篡改检测和宿主回调。

运行：

```sh
go test ./...
go vet ./...
```
