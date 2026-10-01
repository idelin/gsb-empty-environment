# weighted-sampler

精确有理数权重的加权抽样库。Python 3.11，仅标准库，unittest 测试。

## 运行

    python3.11 -m unittest discover -s tests -t . -v   # 测试
    python3.11 demo.py                                  # 运行样例

## 设计

- **精确有理数权重**：权重只接受 int / Fraction（拒绝 float，必须非负，
  负数报 ValueError）。内部维护所有分母的公共分母 D（lcm，单调不减），
  Fenwick 树存整数权重 W_i = w_i * D，更新/抽样 O(log n)。
- **无偏抽样**：randbelow 用拒绝采样（取 k = bit_length(n-1) 个随机位，
  大于等于 n 则重抽），每个整数概率精确为 1/n，无取模偏差。抽中槽位 i
  的概率精确等于 W_i / sum W_i = w_i / total。
- **权重 0 永不选中**：零权重槽位对应空区间，lower_bound 不可达。
- **零总权重明确失败**：sample() / draw() 抛 EmptySamplerError。
- **无放回批量抽取**：Transaction.draw() 把已抽中项权重置 0（键保留），
  因此不会重复；有序序列 (i_1..i_k) 的条件概率明确为
  prod_j w_{i_j} / (T - w_{i_1} - ... - w_{i_{j-1}})，
  由 tx.probability() 与 sequence_probability() 精确给出。
- **快照版本并发**：begin() 捕获版本号与状态副本，事务在私有快照上
  操作；commit() 时若版本已变（期间有更新）抛 StaleTransactionError
  （乐观并发）；rollback() 丢弃副本。
- **随机状态消耗规则（明确）**：commit() 消耗随机位（随机源保持前进）；
  rollback() 把随机源恢复到 begin() 时的状态，即取消的事务不消耗任何
  随机性。随机源由调用方注入（SeededRandomSource(seed) 记录 seed，或
  测试用 BitSource 固定比特流），需实现 randbits(k) 与 getstate/setstate。

## 测试方法学（不用频率近似论证无偏性）

1. **randbelow 精确均匀**（tests/test_sources.py）：枚举全部比特前缀，
   拒绝重抽的尾部概率精确等于 q^m（几何级数），条件分布精确等于 1/n。
2. **秩到键的映射精确**（tests/test_distribution_exact.py）：单抽端到端
   枚举比特空间（拒绝结构齐次，精确尾部）；无放回多抽枚举全部均匀秩
   序列，每个有序序列的概率精确等于条件概率公式。结合 1 即得整体无偏
   的精确论证。
3. **动态增删与回滚对照**（tests/test_model.py）：固定种子（20261001）
   生成 800 个随机操作（add/update/remove/sample/事务提交/回滚/过期
   提交），与朴素累积数组参考实现逐步比对权重、总量、版本号与全部
   抽样结果。
4. **区间边界**（tests/test_sampler.py）：更新（含置零、删除、分母缩放）
   后对所有秩 r in [0, N) 校验 lower_bound 与朴素累积数组一致。
5. **固定随机数手工复算**：固定比特流推出确定结果（如权重 1/2/3、
   比特 101 -> r=5 -> 第 3 个键），含拒绝重抽路径。

经验频率只出现在 demo.py 第 7 节，明确标注为经验估计，不作为无偏性证据。

## 测试结果

    Ran 38 tests in 0.262s
    OK

## 文件

- weighted_sampler/sources.py — 随机源注入、拒绝采样 randbelow
- weighted_sampler/sampler.py — Fenwick 树、权重管理、快照事务
- tests/ — 精确枚举、模型对照、边界与事务测试
- demo.py — 分节运行样例（精确结论 / 经验估计分别标注）
