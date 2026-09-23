# dictcomp — 基于有限样本字典的压缩器

从有限训练样本中选择重复片段组成**固定字典**，用字典引用编码样本。
只使用 Go 标准库（不调用 `compress/*` 等同功能压缩库），选择过程完全
确定，报告包含字典开销的真实压缩对照。

## 设计要点（与需求逐条对应）

- **预算含片段开销**：每个字典条目在文件头中占 `2 + len` 字节（u16 长度
  + 原文），引用 token 固定 2 字节。所有收益计算和报告都扣除了这些开销。
- **选择确定**：样本先按内容排序（结果只依赖样本多重集合，与输入顺序
  无关）；候选按 `(收益降序, 长度降序, 字节序)` 全序排序；自动机转移按
  字节序遍历。同一输入多次运行产出字节级一致的字典。
- **联合计收益，避免重叠双计**：字典片段的取舍不按局部频次估算，而是
  用真实解析成本评估。解析（`OptimalParse`）是 DP 最短路径，选中的匹配
  天然构成**兼容（互不重叠）集合**；贪心选择用每样本的已占用区间表
  （`intervalSet`）保证后续片段只统计与已选匹配兼容的出现次数。
- **小输入精确，大输入启发式**：
  - 精确模式（候选 ≤ 12 且训练语料 ≤ 1024 字节）：枚举候选的**所有子集**，
    每个子集用最优解析求值，返回可证明最优的字典与编码（空集也在枚举中，
    因此"不压缩"总是备选）。
  - 启发式模式：支配过滤后的候选按乐观收益排序，贪心接受"兼容出现
    次数 × (长度−2) > 存储开销"的片段。给出**可行结果与上界**，不承诺最优。
  - 上界推导：任何字典条目的任何匹配都落在某个极大（未被支配）重复片段
    u 的某次出现内；在 u 的一次出现内，兼容匹配集合至多节省 `len(u)−2`
    字节（每个匹配 token 成本 2）。因此
    `总收益 ≤ Σ_u count(u)·(len(u)−2) + 字面运行控制字节数`，
    对任意字典成立（区域重叠只会放大该和，保持上界合法）。
- **训练/验证分别报告**：`train` 命令分别报告训练集与验证集的压缩率，
  两者都包含字典开销（归档模型：一个文件头 + 字典 + 各样本 token 流），
  从不只报局部频次收益。
- **诚实报告**：若最优/贪心结果不优于空字典，则丢弃字典并明确打印
  `no fragment repaid its storage`；压缩率 > 1（膨胀）也如实输出。
- **枚举参考**：`internal/comp/brute.go` 是独立的指数级参考实现（无分数
  过滤的全子集枚举 + 独立递归解析器），测试用它交叉验证精确模式与 DP
  解析器。
- **独立解码器**：`cmd/dictdec` 只依赖格式包 `internal/wire`，不依赖任何
  压缩器代码。

## 文件格式（版本 1）

压缩文件 `DZC1`，整数均为小端：

| 偏移 | 长度 | 字段 |
|---|---|---|
| 0 | 4 | magic `"DZC1"` |
| 4 | 4 | u32 字典条目数 D（0..255） |
| 8 | .. | D 个条目：u16 长度 L + L 字节原文 |
| .. | 8 | u64 未压缩长度 N |
| .. | 4 | u32 未压缩内容的 CRC-32 (IEEE) |
| .. | 4 | u32 token 流长度 T（字节） |
| .. | T | token 流 |

token 流：每个 token 以控制字节 c 开头——`0xFF` 表示字典引用（下一字节
为条目索引）；`0x01..0xFE` 表示该长度的字面运行（后跟 c 字节原文）；
`0x00` 保留（非法）。

字典文件 `DZD1`：`"DZD1"` + u32 条目数 + 各条目（u16 长度 + 字节） +
对前述内容的 u32 CRC-32。

**解码输出有界**：解码器先校验 magic、条目数上限、声明长度 N ≤
`-maxout`（默认 1 GiB）且 N 与 token 流长度 T 物理上相容，然后才按 N
分配输出；解码必须恰好产生 N 字节、恰好消耗 T 字节，最后校验 CRC-32。
任何截断、尾随垃圾、非法控制字节、越界索引都会被拒绝。

## 构建与使用

```sh
go build ./cmd/dictcomp ./cmd/dictdec

# 训练（训练集与验证集分开给出；模式 auto|exact|greedy）
./dictcomp train -train examples/train -val examples/val -dict dict.dzd \
    -min 4 -max 32 -mode auto

# 编码 / 解码（每个压缩文件自包含字典）
./dictcomp encode -dict dict.dzd -in examples/val/node-1.log -out node-1.dzc
./dictdec -in node-1.dzc -out node-1.out        # 独立解码器
```

`examples/gen.sh` 可重新生成示例语料（确定性内容）。

## 运行样例（实际输出）

重复日志语料（贪心模式，训练/验证分别报告，均含字典开销）：

```
$ dictcomp train -train examples/train -val examples/val -dict /tmp/demo.dzd
mode:            greedy (heuristic: feasible result, not guaranteed optimal)
candidates:      187465 repeated fragments (12025 maximal after dominance filtering)
dictionary:      11 entries, 177 bytes storage (counted in every total below)
train:           4 samples, 56004 bytes -> 19985 bytes (ratio 0.3568, dictionary included)
validation:      2 samples, 27822 bytes -> 13451 bytes (ratio 0.4835, dictionary included)
gain bound:      any dictionary gains at most 1813034 bytes on train; achieved 36266 (2.0% of bound)
size floor:      no dictionary compresses train below 0 bytes; achieved 19985
dictionary file: /tmp/demo.dzd
```

编码 + 独立解码器往返：

```
$ dictcomp encode -dict /tmp/demo.dzd -in examples/val/node-1.log -out /tmp/node-1.dzc
encode: 13971 -> 6886 bytes (ratio 0.4929, dictionary embedded in file)
$ dictdec -in /tmp/node-1.dzc -out /tmp/node-1.out
dictdec: 6886 -> 13971 bytes (length and CRC-32 verified)
$ cmp examples/val/node-1.log /tmp/node-1.out && echo OK
OK
```

小输入精确模式（可证明最优；也如实展示：15 字节输入付不起格式开销，
ratio > 1 是真实结论）：

```
$ dictcomp train -train examples/tiny -dict /tmp/tiny.dzd -min 3 -max 8 -mode exact
mode:            exact (exhaustive: provably optimal dictionary and encoding)
candidates:      9 repeated fragments (2 maximal after dominance filtering)
dictionary:      1 entries, 5 bytes storage (counted in every total below)
train:           2 samples, 15 bytes -> 39 bytes (ratio 2.6000, dictionary included)
bound:           train total is provably optimal (39 bytes)
```

精确模式拒绝过大输入（不偷偷降级）：

```
$ dictcomp train -train examples/train -mode exact
dictcomp: exact mode infeasible: 187465 candidates (max 12), 56004 train bytes (max 1024)
```

不可压缩数据（随机字节）：如实报告空字典与膨胀率。

```
mode:            greedy (heuristic: feasible result, not guaranteed optimal)
candidates:      0 repeated fragments (0 maximal after dominance filtering)
dictionary:      0 entries, 0 bytes storage (counted in every total below)
note:            no fragment repaid its storage; dictionary is empty, data is stored as literals
train:           4 samples, 20000 bytes -> 20104 bytes (ratio 1.0052, dictionary included)
gain bound:      any dictionary gains at most 80 bytes on train; achieved 0 (0.0% of bound)
size floor:      no dictionary compresses train below 20024 bytes; achieved 20104
```

## 测试

`go test ./...`（20 个测试）覆盖：

- 格式往返、坏 magic / 坏 CRC / 截断 / 尾随垃圾 / 非法控制字节 / 越界
  索引的拒绝；声明长度超限时拒绝且**不分配**超限输出；
- DP 解析成本与暴力递归解析器在 300 个随机用例上逐一相等；
- 精确选择 vs 独立枚举参考（`BruteBest`）在 60 个小语料上逐一相等；
- 样本顺序打乱后字典与训练总量不变（确定性）；
- 贪心结果可行（往返一致）、不劣于不压缩、且不低于认证下界；
- 随机（不可压缩）数据上如实返回空字典。

实际运行结果：

```
$ go test ./... -count=1
ok  	dictcomp/internal/comp	2.907s
ok  	dictcomp/internal/wire	0.694s
```

其中精确模式对照日志：`compared 60 exact cases against the enumeration reference`。

## 限制（如实说明）

- 精确模式限定候选 ≤ 12 且训练语料 ≤ 1024 字节；超出时 `-mode exact`
  直接报错，`auto` 降级为贪心。
- 上界在高度冗余数据上可能很松（极大片段的区域互相重叠，和式上界会
  放大）；它是合法上界而非紧界。
- 候选扫描对超过 4 MiB 的单样本按确定性步长抽样（仅影响启发式候选，
  不影响所报告数字的真实性）。
- 字典条目上限 255（引用 token 为 1 字节索引），条目长度上限 1024。
