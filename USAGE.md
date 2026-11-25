# 自定义素数阶群对数内积证明包

根包 `ipp` 提供证明生成、严格线格式、转录挑战重建、单核验和批量核验。群阶、点解析/规范编码、点加和标量乘由可信 `ipp.Adapter` 提供；包内只保存适配器输出的规范点字节。

```go
ad := ipp.NewP256Adapter()
gens, err := ipp.DeterministicP256TestGenerators(8, []byte("host-fixed-ceremony"))
if err != nil {
    panic(err)
}

// a、b 必须已经是 [0,q) 内的规范标量；零向量是合法见证。
P, proof, err := ipp.Prove(ad, []byte("host/tenant/context"), gens, a, b)
if err != nil {
    panic(err)
}

raw, err := proof.Encode(ad)
if err != nil {
    panic(err)
}
decoded, err := ipp.DecodeProof(ad, raw) // 严格长度、轮数、标量和规范点检查
if err != nil {
    panic(err)
}

err = ipp.Verify(ad, []byte("host/tenant/context"), gens, P, decoded)
```

也可使用 `ipp.GenerateProof` 校验宿主已有的 `P` 确实由见证打开，或用 `ipp.VerifyStatement`、`ipp.VerifyBatch`、`ipp.VerifyLinearBatch` 接入服务。线性批处理仍会逐项重建全部 Fiat-Shamir 挑战、检查每轮挑战非零并求逆，然后才对最终误差点取独立且不重复的随机线性组合；不会用一个批量系数替代各证明自己的挑战。

## 转录与折叠

转录依次写入并对每个字段加 4 字节大端长度前缀：

- 域标签（空时使用 `ipp.DefaultDomainLabel`）；
- 宿主上下文；
- `n` 的 4 字节大端表示；
- 全部 `G_i`、全部 `H_i`、`U` 和宿主承诺 `P` 的规范编码；
- 截至当前轮次的有序 `L_j`、`R_j`。

挑战摘要按大整数解释并对素数阶 `q` 取模；若结果为零，则追加从 1 开始递增的 8 字节大端计数器并重哈希。核验始终从宿主固定的生成元和 `P` 重建每轮挑战，再执行 `P+x²L+x⁻²R`、生成元折叠和末轮 `A G+B H+AB U` 检查，不接受调用方传入的“已折叠路径”或挑战列表。

线格式额外携带初始转录前缀的 SHA-256 标签，使没有折叠轮的 `n=1` 单元素打开也能识别域、上下文、`n`、生成元或 `P` 被替换。

## 标量和点校验

- 线格式标量使用 `ceil(bitlen(q)/8)` 字节定长大端编码，必须满足 `0 <= s < q`；负数、溢出、长度错误均拒绝。
- 挑战、逆元和批量权重必须位于 `1..q-1`；零挑战按转录计数器重试，异常零值由 `ErrZeroScalar` 标识。
- 最终打开标量中的零不能被一概拒绝，否则全零见证不可能合法。全零见证产生规范单位元 `P/L/R` 和零 `A/B`，并可通过核验；向非零证明注入零打开标量会破坏末轮等式，错误同时包装 `ErrZeroScalar` 与 `ErrVerificationFailed`。
- `P`、`L`、`R` 的规范单位元编码允许；宿主固定的 `G_i`、`H_i`、`U` 必须为非单位元。
- P-256 适配器使用 33 字节压缩 SEC1 编码，单位元为 33 个零字节；非规范/不在曲线上的编码会被拒绝。小阶参考群默认使用素数阶 11 和单字节规范点，仅用于代数测试。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖 `n=1,2,4,8,16,32`、P-256 `n=1/2/4/16/1024`、全零见证、交换 `L/R`、修改 `n`、替换上下文、注入零或 `q` 标量、非规范点、错误轮数和逐字节截断。`DeterministicP256TestGenerators` 使用已知离散日志的确定性生成元，只适合测试；P-256 适配器和这些生成元不替代生产曲线参数、生成元仪式或安全评审。
