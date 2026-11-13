# 多点多项式承诺验证包

供 Go 接入宿主调用的多点 KZG 风格验证层。请求方只提交 `C`、1–32 组 `(z,y)`
与 `pi`；`q`、G1/G2 运算、配对、规范编码和同一 `tau` 的幂 SRS 全部由可信宿主
提供。`EncodedRequest` 不含 SRS，验证器也不会从请求中读取任何 SRS。

## 接入

宿主实现 `Host[G1, G2, GT]`，提供：

- `PrimeModulus`、标量规范编解码；
- `Group[P]` 的生成元、单位元、加减、标量乘、相等/单位元判断、规范编解码和点校验；
- `Pairing` 的配对值与相等判断。

可信宿主用 `NewSRS(identity, g1Powers, g2Powers)` 装载 `tau^0..tau^d`，再用
`NewVerifier` 绑定。`identity` 是宿主选定的稳定 SRS 标识，会随成功结果返回。

```go
setup, err := netsec.NewSRS("setup-transcript-digest", g1Powers, g2Powers)
verifier, err := netsec.NewVerifier(host, setup)

result, err := verifier.Verify(netsec.EncodedRequest{
    Commitment: commitmentBytes,
    Proof:      proofBytes,
    Evaluations: []netsec.EncodedEvaluation{
        {Z: zBytes, Y: yBytes},
        // ...
    },
})
```

## 验证语义

- 输入必须有 1–32 组求值；空请求返回 `ErrEmptyRequest`，绝不视为成功。
- 标量先经宿主解码，再强制落在 `[0, q-1]` 且字节必须是宿主规范编码。
- 相同 `z` 且相同 `y` 合并；相同 `z` 不同 `y` 返回 `ErrConflictingEvaluation`。
- 合并后按 `z` 升序构造唯一插值多项式 `I` 与
  `Z(X)=∏(X-z)`，验证
  `e(C-CommitG1(I), G2) = e(pi, CommitG2(Z))`。
- 验证所需的 SRS 次数必须足够：`SRS.MaxDegree()` 是 G1/G2 两组幂序列共同支持的
  次数，`k` 个去重求值点要求次数至少为 `k`，否则返回 `ErrSRSDegree`。
- `CommitG2(Z)` 是单位元时返回 `ErrDegenerateCommitment`，不进行配对。
- 除上述 `Z` 承诺外，规范的 G1 单位元 `C` 或 `pi` 是合法输入；例如零多项式的
  `C` 与 `pi` 都为单位元，可正常通过。
- 所有请求点先解码、校验并检查规范编码，之后才做群承诺或配对；非法点返回
  `ErrInvalidPoint`/`ErrNonCanonicalPoint`。
- 成功结果包含去重且按 `z` 排序的 `Evaluations`、`SRSIdentity` 与 `SRSMaxDegree`。

## 测试边界

`verify_test.go` 使用已知 `tau` 的小域配对代数替身：两个源群是 `Z_q` 加法群，
目标群同样按加法书写，配对模型为 `e(aG1,bG2)=a*b mod q`。该替身精确满足双线性
关系，用于穷举低次多项式、重复点、零多项式、错误证明、次数不足以及编码边界。

它**不**模拟椭圆曲线点、子群检查、真实配对或密码学安全性；这些必须由生产宿主
的真实实现负责。测试验证的是本包的代数流程与编码边界。
