# hashring — 一致性哈希质量检验诊断内核

对任意哈希函数 + 节点序列构建一致性哈希环，用数百万随机样本打点映射，
输出节点负载标准差、最大最小比值与卡方拟合度检验报告，并通过位宽-雪崩
偏差曲线精准定位哈希函数（如 FNV-1a）在特定位宽下的雪崩不足缺陷。

仅使用 Go 标准库，无任何第三方依赖。

## 结构

| 文件 | 职责 |
|---|---|
| `hashes.go` | MD5-32、MurmurHash3 x86_32（手写实现）、FNV-1a-32 |
| `ring.go` | 一致性哈希环（虚拟节点、二分定位、弧长份额计算） |
| `stats.go` | 卡方统计量与 p-value（正则化不完全伽马函数） |
| `analyzer.go` | 采样打点、负载统计、雪崩检验、位宽-偏差曲线 |
| `cmd/analyze/main.go` | 命令行诊断报告 |
| `analyzer_test.go` | `go test` 验收测试 |

## 运行

```sh
go run ./cmd/analyze                          # 默认 10 节点 × 160 虚拟节点 × 200 万样本
go run ./cmd/analyze -samples 5000000 -nodes 20 -vnodes 200
go test ./... -v                              # 运行验收测试
```

## 关键设计：卡方检验的期望值

朴素做法把卡方期望设为 `样本数/节点数`，但虚拟节点在环上的布局本身有
随机性（相对偏差 ~ 1/√vnodes），大样本下会把"环不平衡"误判为"哈希不均匀"。
本内核改为按**环上弧长份额**（`Ring.Shares()`）计算期望，隔离布局随机性，
使卡方检验真正度量哈希映射的均匀性；负载标准差与最大最小比则单独刻画
环的平衡度。

## 运行样例（`go run ./cmd/analyze`，200 万样本）

```
--- 随机键负载分布 ---
[MD5-32]              标准差 16281.52 (cv 8.141%)  最大最小比 1.2875  chi2=5.28  p=8.09e-01  均匀
[MurmurHash3-x86_32]  标准差 13597.78 (cv 6.799%)  最大最小比 1.2559  chi2=5.30  p=8.07e-01  均匀
[FNV-1a-32]           标准差 63770.73 (cv 31.885%) 最大最小比 3.6452  chi2=9.63  p=3.81e-01  均匀

--- 顺序键负载分布（key-00000000 ...，低熵真实负载） ---
[MD5-32]              chi2=9.24    p=4.16e-01   均匀
[MurmurHash3-x86_32]  chi2=4.38    p=8.85e-01   均匀
[FNV-1a-32]           chi2=153.05  p=2.06e-28   不均匀

=== 位宽-雪崩偏差曲线（低 w 位输出比特翻转概率与 0.5 的偏差）===
[FNV-1a-32]
 width    meanDev     maxDev  badBits
     4     0.2804     0.3750        4
     8     0.1718     0.3750        5
    16     0.0922     0.3750        5
    32     0.0767     0.3750        5
（MD5 / MurmurHash3 全部位宽 meanDev ≤ 0.0004，badBits = 0）

=== 雪崩效应总览 ===
[FNV-1a-32] 平均偏差 0.0767 | 最大偏差 0.3750 (bit 0) | 异常位: 5/32
  bit0───────────────────────bit31: XXXXX...........................
```

## 诊断结论

- **MD5 / MurmurHash3**：随机键与顺序键下卡方检验均通过；32 个输出位
  雪崩翻转概率全部贴近 0.5，位宽曲线平坦，无缺陷。
- **FNV-1a**：随机键的边缘分布勉强均匀（chi2 通过），但暴露出两处缺陷——
  1. **环构建质量差**：虚拟节点标签是低熵顺序字符串，FNV 雪崩不足导致
     环点聚集，负载标准差约为 MD5 的 4 倍，最大最小比 3.65（MD5 仅 1.29）；
  2. **低位雪崩不足**：乘法只向高位传播，低位输出比特翻转概率严重偏离
     0.5（bit0 偏差 0.375），位宽曲线在 4~8 位处出现明显峰值（meanDev
     0.28/0.17），顺序键负载下卡方检验以 p≈2e-28 显著拒绝均匀假设。

## 测试结果（`go test ./...`）

```
--- PASS: TestMurmur3KnownVectors                    规范参考向量
--- PASS: TestFNV1aKnownVectors                      规范参考向量
--- PASS: TestRingCountsSumToSamples                 打点计数守恒
--- PASS: TestRingSharesSumToOne                     弧长份额归一
--- PASS: TestHighQualityHashesAreUniform            MD5/Murmur 卡方通过 (p=0.53/0.27)
--- PASS: TestFNVSequentialKeyDeficiency             FNV 顺序键 chi2=92.2 p=5.9e-16，5倍于 Murmur
--- PASS: TestFNVLowBitWidthAvalancheDeficiency      FNV 4位 meanDev=0.28，3倍于其32位值
--- PASS: TestAvalanche                              FNV 5 个异常位且最差位在低位 (bit 0)
--- PASS: TestChiSquarePValueKnownValues             卡方 p-value 数值正确性
ok      hashring
```
