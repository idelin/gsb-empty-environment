# ARC封印链实例连续性

攻击者在转发途中插入、删除或调换 ARC 集，想让断链显示为通过。为Go收件端写可导入的 ARC 链校验包：实例号须从1连续到N，每个 i 恰有 AS、AMS、AAR 各一；i=1 的 AS 其 cv 只能是 none，i>1 只能是 pass 或 fail；任一 cv=fail 或最新 AMS 验签失败则整链 fail；AS 签名覆盖 i 及以下全部 ARC 头。验签函数由调用方注入，测缺号、重复、cv 错位和调换。

## `arc` 包

导入路径 `example.org/netsec/q0266/arc`。密码学验签通过 `Verifiers` 注入，
包本身只负责结构与策略校验。

```go
import "example.org/netsec/q0266/arc"

res, err := arc.ValidateChain(headers, arc.Verifiers{
    VerifySeal:             myDKIM.VerifyARCSeal,
    VerifyMessageSignature: myDKIM.VerifyARCMessageSignature,
})
switch res.Status {
case arc.Pass:   // 链完整且全部签名通过
case arc.Fail:   // 见 res.Failures，可用 errors.Is 匹配 arc.ErrMissingInstance 等哨兵错误
case arc.NoChain:
}
```

校验规则：

- 实例号必须连续覆盖 1..N，每个实例恰有一个 `ARC-Seal`、`ARC-Message-Signature`、
  `ARC-Authentication-Results`，重复头报 `ErrDuplicateHeader`。
- i=1 的 AS 仅允许 `cv=none`；i>1 仅允许 `cv=pass`/`cv=fail`，任一 `fail` 即整链 fail。
- 结构合法后才进入验签：每个 AS(i) 对 `SealCoveredHeaders`（实例 1..i 的全部
  ARC 头，排除 AS(i) 自身）验签；最新 AMS(N) 对 `AMSCoveredHeaders`（全部
  AAR 加 AMS(N) 自身）验签，任一失败整链 fail。
- 头的物理顺序不影响结论；解析支持 RFC 5322 折行。

运行 `go test ./...` 查看缺号、重复、cv 错位与调换攻击的测试。
