# netsec: 对数长度内积证明

在可信适配器提供的素数阶群上实现自定义内积承诺证明的生成与核验。
承诺形式：

```
P = Σ a_i·G_i + Σ b_i·H_i + <a,b>·U
```

`n` 为不超过 1024 的二次幂，证明含 `log2(n)` 个 `L/R` 对与末轮两个标量。

## 协议

- 每轮均分向量，计算
  `L = <aL,GR> + <bR,HL> + <aL,bR>·U`，
  `R = <aR,GL> + <bL,HR> + <aR,bL>·U`。
- 挑战 `x`：SHA256 作用于长度前缀转录（域标签、宿主上下文、`n`、
  全部生成元、`P`、截至本轮的有序 `L/R` 编码）后模 `q`；结果为零则
  附加计数器重哈希，保证 `x` 非零可逆。
- 折叠：`a = x·aL + x⁻¹·aR`，`b = x⁻¹·bL + x·bR`，
  `G = x⁻¹·GL + x·GR`，`H = x·HL + x⁻¹·HR`，
  承诺更新 `P ← P + x²·L + x⁻²·R`。
- 末轮核对单元素承诺 `P == a·G + b·H + a·b·U`。

核验方完整重建每一轮挑战并折叠生成元与承诺，绝不只检查最终等式。
轮数不符（`ErrRoundCount`）、非规范标量或点编码（解码报错）、
截断或拖尾字节（`ErrEncoding`）、转录上下文不符（`ErrVerification`）
均被拒绝。生成元必须非单位元（`ErrIdentityGenerator`）；`P/L/R`
允许为单位元，零向量是合法见证。

## 用法

```go
P, proof, err := netsec.Prove(g, params, a, b)   // g 为 Group 适配器
err = netsec.Verify(g, params, P, proof)
err = netsec.VerifyBytes(g, params, P, proof.Marshal(g))
```

`Params{G, H, U, Context}` 由宿主固定；`Context` 将证明绑定到宿主上下文。

## 适配器

- `adapters/p256`：P-256 测试适配（素数阶、余因子 1），点编码为
  33 字节（压缩 SEC-1，全零保留给单位元）。
- `adapters/smallprime`：小阶代数参考（`Z/65521Z` 加法群），无任何
  密码学强度，仅用于独立交叉验证证明代数。

**安全声明**：以上适配器仅用于一致性测试，不声称替代生产环境的
群参数与编码审查；生产接入须使用经审查的群适配器。

## 测试

```sh
go test ./...
```

覆盖：多 `n` 往返、零见证/单位元承诺、证明确定性，以及交换 `L/R`、
改 `n`、轮数增删、换上下文、换生成元、截断/拖尾、非规范标量/点、
篡改末轮标量、非法 `n`、单位元生成元等拒绝路径。
