# hashring-analyzer

一致性哈希算法质量检验诊断内核（纯 Go 标准库实现，无第三方依赖）。

输入任意哈希函数与节点序列，在可配置位宽的一致性哈希环上对数百万个
样本密钥进行映射打点，输出：

- **节点负载标准差 / 变异系数 (CV)**
- **最大最小负载比 (max/min)**
- **卡方拟合优度检验**（期望值取自环上各节点弧长比例，因此检验的是
  哈希函数本身的均匀性，而非虚拟节点布局的偶然不平衡；p 值由正则化
  不完全伽马函数精确计算）
- **方差曲线**：约化卡方 chi2/df 与 max/min 负载比随环位宽 (16..64 bit)
  的变化曲线，用于定位分布缺陷出现的位宽
- **雪崩效应分析**：翻转每个输入比特，统计 64 个输出比特的翻转概率
  （严格雪崩准则，理想值 0.5），精确定位缺陷比特位

内置对比 **MD5**、**MurmurHash3 (x64_128)**、**FNV-1a** 三种哈希，
并精准识别出 FNV-1a 的雪崩不足缺陷：末字节熵最远只能到达输出比特 17，
比特 18-63 完全不受密钥后缀影响，导致一致性环（位置取自哈希高位）在
所有位宽下分布坍塌。

## 结构

```
hashfn/    三种 64 位哈希函数 (MD5 折叠 / MurmurHash3 x64_128 / FNV-1a)
ring/      可配置位宽的一致性哈希环 (虚拟节点、弧长比例)
analyze/   采样统计、卡方检验 (含 gammaP)、方差曲线、雪崩分析
cmd/analyzer/  诊断报告 CLI
```

## 运行

```sh
go run ./cmd/analyzer                 # 默认: 10 节点 x 160 虚拟节点, 3M 样本
go run ./cmd/analyzer -samples 5000000 -nodes 12 -replicas 200
go test ./...                         # 全部单元测试
```

参数：`-samples`（每组样本数，默认 3,000,000）、`-curve-samples`
（方差曲线每个位宽点的样本数，默认 1,000,000）、`-nodes`、`-replicas`、
`-avalanche-samples`。

## 运行样例（节选）

```
[1] 基准均匀性: 3,000,000 随机密钥, 64-bit 环
    MD5           stddev=20202.8   CV= 6.73%  max/min=1.209  chi2=7.193   df=9 p=0.6170  PASS
    MurmurHash3   stddev=13515.8   CV= 4.51%  max/min=1.142  chi2=6.395   df=9 p=0.6999  PASS
    FNV-1a        stddev=153636.5  CV=51.21%  max/min=5.982  chi2=10.626  df=9 p=0.3022  PASS

[2] 顺序密钥: 8-byte 大端计数器 0..N-1, 64-bit 环
    MD5           stddev=20179.3   CV= 6.73%  max/min=1.209  chi2=8.852   df=9 p=0.4510  PASS
    MurmurHash3   stddev=13393.3   CV= 4.46%  max/min=1.132  chi2=21.320  df=9 p=0.0113  PASS
    FNV-1a        stddev=159466.8  CV=53.16%  max/min=7.162  chi2=78198.9 df=9 p=<1e-300 FAIL

[3] 方差曲线: 约化卡方 chi2/df vs 环位宽 (顺序密钥)
    width | MD5           | MurmurHash3   | FNV-1a        |
    -------------------------------------------------------
       16 |   0.37        |   0.71        | F 24689.32    |
       32 |   0.34        |   0.65        | F 24658.14    |
       64 |   0.34        |   0.65        | F 24658.14    |
    FNV-1a : 位宽 [16 24 32 40 48 56 64] 全部失败

[4] 雪崩效应 (行=输入字节, 列=输出比特组 高->低, 理想值 0.50)
    FNV-1a:
      in[6]    0.08    0.49    0.19    0.00    0.08    0.47    0.51    0.33
      in[7]    0.00    0.03    0.22    0.00    0.00    0.05    0.47    0.39
      最大偏差 0.50 (输入bit 0 -> 输出bit 0)
      缺陷: 末字节熵无法到达输出比特 18-63 (共 36 位)
```

要点解读：

- FNV-1a 即使用完全随机的密钥，虚拟节点位置也会成簇（位置由同一哈希
  产生），负载 max/min ≈ 6，而 MD5/MurmurHash3 ≈ 1.2。
- 顺序密钥（自增 ID 类，熵集中在末尾字节）下 FNV-1a 卡方检验在
  **所有环位宽** 被拒绝（chi2/df ≈ 2.5×10⁴，理想值 ≈ 1）。
- 雪崩矩阵给出根因：FNV 单字节混合 (XOR + 一次乘法) 只向高位传播，
  末字节熵到不了比特 18-63；比特 0 甚至随输入确定性翻转 (p=1.0)。

## 测试结果

`go test ./...`（Go 1.11，darwin/amd64）：

```
ok  hashring-analyzer/analyze   TestChiSquarePValueKnownValues
                                TestChiSquareCriticalRoundTrip
                                TestGammaPMatchesClosedForm
                                TestLoadStatsArithmetic
                                TestUniformHashPassesChiSquare
                                TestFNVSequentialKeysFailChiSquare
                                TestWidthCurveIsolatesFNVDefect
                                TestFNVLayoutImbalanceWithRandomKeys
                                TestAvalancheDetectsFNVHighBitDeficiency
ok  hashring-analyzer/hashfn    FNV-1a 与标准库 hash/fnv 逐值一致;
                                MurmurHash3 空输入=0、全尾长路径确定且无碰撞
ok  hashring-analyzer/ring      映射确定性、弧长比例和为 1、位宽截断、
                                负载均衡、非法配置 panic
```

共 20 个测试全部通过。关键断言：

- `TestFNVSequentialKeysFailChiSquare`：16-bit 环 + 顺序密钥下 FNV 被
  卡方检验拒绝 (p < 0.001)，且其 chi2 至少为 MD5 的 100 倍；
- `TestWidthCurveIsolatesFNVDefect`：FNV 的约化卡方在窄位宽下高出
  MD5 一个数量级以上，MD5 在所有位宽通过；
- `TestAvalancheDetectsFNVHighBitDeficiency`：FNV 末字节翻转时高位
  输出比特翻转概率 < 0.05（理想 0.5），MD5/Murmur 全部比特落在
  [0.35, 0.65]。
