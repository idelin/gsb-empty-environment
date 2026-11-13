# 四字节末轮密钥候选恢复

这是一个可直接导入 Go 宿主的候选恢复包，仅实现题目中的四字节差分故障模型。恢复结果是末轮密钥的四字节片段，不是完整密码主密钥。

## 模型

- GF(256) 使用模多项式 `0x11b`。
- S 盒对字节先求乘法逆元（`0` 映射为 `0`），再做题目给定的循环移位仿射变换和 `0x63` 异或。
- 正常输出：`C[i] = S(v[i]) XOR K[i]`。
- 故障输出：`F[i] = S(v[i] XOR M[i][j]*d) XOR K[i]`。
- 每组的 `j` 在 `0..3`、`d` 在 `1..255`，不同组可使用不同的 `v`、`j`、`d`；`K` 固定。
- `M` 为：

```text
2 3 1 1
1 2 3 1
1 1 2 3
3 1 1 2
```

## 导入与调用

模块路径为 `example.org/netsec/q0030`，包名为 `netsec`。

```go
import netsec "example.org/netsec/q0030"

observations := []netsec.Observation{
    {
        C: [netsec.KeyBytes]byte{0xdb, 0x5b, 0x98, 0xa9},
        F: [netsec.KeyBytes]byte{0x86, 0x18, 0xb9, 0x19},
    },
    // 最多再提供 7 组
}

result, err := netsec.Recover(observations) // 默认每字节全 0..255 域
if err != nil {
    // 输入为空、超过 8 组等接口错误
    panic(err)
}

switch result.Status {
case netsec.StatusUnique:
    // result.Candidates 只有一个候选
case netsec.StatusNotUnique:
    // 未唯一；必须保留全部候选
case netsec.StatusInconsistent:
    // 给定观测在模型和密钥域内无解
}
```

每个候选附带与输入组一一对应的 `Witness{J, D}`：

```go
type Candidate struct {
    Key       [4]byte
    Witnesses []Witness
}
```

`Key` 按无符号字节字典序排序，`Witnesses[i]` 证明候选能解释第 `i` 组观测。

宿主可限制每字节的密钥候选集合。`nil` 表示该字节使用完整 `0..255` 域，非 `nil` 空切片表示该字节无允许值：

```go
domain := netsec.FullDomain()
domain[0] = []byte{0x2b, 0x2c, 0x7b, 0xd4}
domain[1] = []byte{0x7e, 0x7f, 0x2a, 0x81}
domain[2] = []byte{0x15, 0x14, 0x40, 0xea}
domain[3] = []byte{0x16, 0x17, 0x43, 0xe9}

result, err := netsec.RecoverWithDomain(observations, domain)
```

## 算法

1. 预计算正逆 S 盒，以及逆 S 盒差分表：对输出差分 `D`，记录所有满足 `InvS(y) XOR InvS(y XOR D) = delta` 的 `y`。
2. 对一组 `(C,F)`，枚举 4 个故障位置 `j` 和 255 个非零 `d`；逐字节查表并以 `K[i] = C[i] XOR y` 生成字节候选，按宿主域过滤后做笛卡尔组合。
3. 对后续各组生成候选集合并与当前存活集取交集；每个保留候选记录首组的 `(j,d)` 见证。
4. 输出按字节序排序的全部候选，不遍历 `2^32` 个密钥。

对每组观测，所有四个字节都必须满足同一个 `(j,d)`，且输出差分非零；`C == F`、仅一个字节有差分等情况判为模型不一致。

## 测试

```sh
go test ./...
```

测试包括：

- 已知本地 `K` 按题面公式合成 8 组观测，验证唯一候选与见证。
- 单字节无效故障、零故障、单候选之外的重复观测。
- 多候选明确标记为未唯一。
- 每字节仅 4 个允许值时，对 `4^4` 小域全组合穷举，与差分候选生成结果逐项交叉验证。
