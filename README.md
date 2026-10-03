# hashring-analyzer

一致性哈希算法质量检验诊断内核：对任意哈希函数与节点序列，在一致性哈希环上
进行大规模随机样本映射打点，输出节点负载标准差、最大/最小负载比与卡方拟合
优度评估，并通过**位宽方差曲线**精准定位哈希函数的雪崩效应缺陷。

零依赖纯 TypeScript，使用 Node.js 原生 TypeScript 支持与内置测试运行器。

## 环境要求

- Node.js **>= 23.6**（原生运行 `.ts`，内置 `node:test`）；仓库附 `.nvmrc`
- 无任何 npm 依赖，无需 `npm install`

```bash
nvm use            # 或任意 >= 23.6 的 Node
npm test           # node --test tests/*.test.ts
npm run analyze    # node src/cli.ts（默认 200 万样本）
```

## 运行样例

```bash
# 默认：200 万随机样本、50 节点、每节点 160 虚拟点，对比 MD5 / MurmurHash3 / FNV-1a
node src/cli.ts

# 顺序结构化键（暴露弱雪崩哈希缺陷的最坏情形）
node src/cli.ts --keys sequential

# 自定义规模
node src/cli.ts --samples 5000000 --nodes 100 --vnodes 160 --keys hex --max-width 18
```

参数：`--samples N` `--nodes N` `--vnodes N` `--seed N`
`--keys random|sequential|hex` `--max-width BITS`。

## 样例输出（200 万样本，50 节点 × 160 虚拟点）

### 随机键（`--keys random`）

```
--- Node load balance on the consistent-hash ring ---
hash                         mean     stddev      cv%  cv/theory       min       max   max/min       chi2   coll  verdict
MD5-32                    40000.0    3090.02     7.73       0.98     34651     48844     1.410    11935.3      0     PASS
MurmurHash3-x86-32        40000.0    2547.34     6.37       0.81     33469     45525     1.360     8111.2      0     PASS
FNV-1a-32                 40000.0   11431.75    28.58       3.62     16266     62038     3.814   163356.1      0     FAIL
```

即使键完全随机，FNV-1a 的环负载 CV 仍是理论下限的 3.62 倍：环上虚拟点
由 `"node-i#v"` 这类结构化字符串放置，FNV 雪崩不足导致点位置聚集，
**环几何本身已被破坏**。

### 顺序键（`--keys sequential`）—— 位宽方差曲线

```
  FNV-1a-32
     4 bits | buckets       16| chi2/df      0.013| p     1.0000  <-- OVER-REGULAR (structural artifact)
     6 bits | buckets       64| chi2/df      0.007| p     1.0000  <-- OVER-REGULAR (structural artifact)
     8 bits | buckets      256| chi2/df      8.214| p  6.93e-286  <-- DEFICIENT (avalanche failure)
    10 bits | buckets     1024| chi2/df      2.398| p  6.68e-119  <-- DEFICIENT (avalanche failure)
    12 bits | buckets     4096| chi2/df      1.180| p   5.81e-15  <-- DEFICIENT (avalanche failure)
    14 bits | buckets     16384| chi2/df     1.001| p     0.4707
    16 bits | buckets    65536| chi2/df      0.961| p     1.0000  <-- OVER-REGULAR (structural artifact)

--- Diagnostic conclusion ---
  MD5-32: uniform at every tested bit width.
  MurmurHash3-x86-32: uniform at every tested bit width.
  FNV-1a-32: AVALANCHE DEFICIENCY at bit width(s) 4, 6, 8, 10, 12, 16
             (worst: 8 bits, chi2/df = 8.21, p = 6.93e-286).
```

MD5 与 MurmurHash3 在全部位宽上 chi2/df ≈ 1.0；FNV-1a 在 **8/10/12 位**
截断时显著非均匀（p 低至 6.93e-286），在 4/6/16 位呈现"过度规则"
（chi2/df ≈ 0.01，顺序键被映射成近乎周期性的低比特模式）——两类异常
都源于 FNV 乘法扩散只向高位传播、缺乏全字混合的雪崩缺陷。

## 方法论

- **环负载评估**：一致性哈希环即使使用理想哈希，节点负载 CV 也有
  理论下限 `1/sqrt(vnodes)`（环弧长服从指数分布）。因此负载判定不与
  完美均匀比较，而是与理论下限比较：`cv/theory ≤ 1.5` PASS，
  `≤ 2.5` WARN，否则 FAIL。同时报告标准差、min/max、max/min 比值、
  卡方统计量与环点碰撞数。
- **位宽方差曲线**：将 32 位哈希值截断至低 `b` 位（b = 4..16），把样本
  打入 `2^b` 个桶，对每个位宽做卡方拟合优度检验（p 值由正则化上不完全
  伽马函数 Q(df/2, χ²/2) 计算，Lanczos + 级数/连分式实现）。双侧判定：
  `p < 1e-3` 判为非均匀（雪崩不足），`p > 1 - 1e-4` 判为过度规则
  （结构性伪影）。
- **性能**：每个样本只哈希一次，结果存入 `Uint32Array`，环映射（二分
  查找）与全部位宽分析复用；200 万样本 × 3 种哈希全量分析约 1 分钟。

## 代码结构

```
src/
  hashes.ts    MD5-32（crypto）、MurmurHash3 x86_32、FNV-1a/FNV-1 实现
  sampler.ts   mulberry32 确定性 PRNG 与键生成器（random/sequential/hex）
  ring.ts      一致性哈希环：虚拟点放置、排序二分查找、碰撞计数
  stats.ts     均值/标准差、卡方统计量、logGamma、gammaQ、p 值
  analyzer.ts  负载分析（analyzeLoad）与位宽方差曲线（analyzeBitWidths）
  report.ts    报告格式化与缺陷结论汇总
  cli.ts       命令行入口
tests/         node:test 测试（27 个用例）
```

## 测试结果

```
$ node --test tests/*.test.ts
ℹ tests 27
ℹ pass 27
ℹ fail 0
```

覆盖：三种哈希的标准测试向量（RFC 1321、MurmurHash3 参考值、FNV 官方
向量）、统计例程（ textbook 卡方临界值、Γ(1,x)=e^-x ）、环语义（全空间
覆盖、环绕、删节点仅迁移 ~1/N 的键）、以及端到端判定（MD5/Murmur 环负载
PASS 且全位宽均匀；FNV-1a 环负载 FAIL 且在 8 位宽被检出雪崩缺陷）。
