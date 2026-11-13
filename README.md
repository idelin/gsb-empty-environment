# CRT故障签名泄露私钥因子

硬件签名回归台需要判断一次CRT计算故障是否足以泄露RSA私钥。给Go安全实验宿主实现可导入的故障分析包：输入公开RSA模数N、指数e、每份样本包含消息代表m和签名值s，另可提供同消息的正常签名s_good，均为非负大整数且小于N；先验证N为奇合数、1<e<N、gcd(m,N)=1。每份签名计算v=s^e mod N，v=m为正常；否则计算g=gcd(v-m mod N,N)，仅1<g<N时返回因子g与N/g，并用乘积及一次Miller-Rabin确定性基集合对64位参数或宿主注入的素性回调做结构说明，不能把合数因子称素数。若g为1或N只记为无法由该样本分解，继续其他样本。支持比较一份正常签名和故障签名时额外计算gcd(s_good-s_bad,N)，两种方法结果须一致或明确报告输入不符合单分支CRT故障模型。重复、全零、对不同消息签名和本身不验签的样本不得造成假成功。提供本地小RSA CRT签名器，可在p支或q支的模幂结果上按调用方位置翻转一位，再重组；穷举小素数、消息和故障位核对所有返回因子真实整除N，并保留不能分解的故障样本证据。

## 包结构

模块 `example.org/netsec/q0074`，根包 `netsec`，无第三方依赖。

- `primality.go`：64 位确定性 Miller-Rabin 基集合 `{2,3,5,7,11,13,17}`、`Primality` 结论与宿主素性回调 `PrimalityCallback`。
- `analyzer.go`：`NewAnalyzer`、`AnalyzeSample`、`AnalyzeBatch`，含参数校验、gcd 分解、乘积校验与失败证据 `Evidence`。
- `pair.go`：`AnalyzePair` 交叉比对 `gcd(v-m,N)` 与 `gcd(s_good-s_bad,N)`，不一致即报告 `PairModelViolation`。
- `signer.go`：本地实验用 `CRTSigner`，`SignWithBitFault` 在指定 CRT 分支余数上翻一位后 Garner 重组；越界位返回 `ErrFaultPosition`。

## 关键语义

- 前置校验：N 为奇合数、`1 < e < N`、`0 <= m,s < N`、`gcd(m,N)=1`，不满足即拒绝，不参与成功统计。
- 仅当 `1 < g < N` 才视为分解成功；`g=1` 或 `g=N` 记为 `StatusUnfactorable` 并保留证据，继续处理后续样本。
- 因子结构同时给出 `G`、`N/G`、`G*Cofactor==N` 校验与两侧素性结论；64 位内为确定性结论，超 64 位且无回调时标 `unknown`，合数因子永远不会被称素数。
- 单分支故障下 gcd 暴露的是**未受影响**分支的模数：p 支故障得到 q，q 支故障得到 p。
- 批处理按 `(m,s)` 去重；全零、异消息、自身不验签样本不会产生假成功。

## 使用示例

```go
analyzer, err := netsec.NewAnalyzer(N, e, nil) // 超 64 位时第三个参数传素性回调
if err != nil { /* N/e 不合法 */ }

res := analyzer.AnalyzeSample(netsec.Sample{M: m, S: badSig})
if res.Status == netsec.StatusFactored {
    // res.Factors.G 与 res.Factors.Cofactor 真实整除 N
}

pair := analyzer.AnalyzePair(m, goodSig, badSig)
// pair.Status == netsec.PairAgree 才符合单分支 CRT 故障模型

signer, _ := netsec.NewCRTSigner(p, q, e)
faulty, err := signer.SignWithBitFault(m, netsec.BranchP, 0)
```

## 测试

- `go test ./...`：单元测试与完整穷举（小素数对 × 公开指数 × 互素消息 × 两分支全部合法故障位）。
- `go test -short ./...`：跳过穷举，仅跑守卫与边界测试。
- 穷举会核对每个返回因子真实整除 N、两种 gcd 方法一致，并保留越界故障位与非互素消息的拒绝证据。
