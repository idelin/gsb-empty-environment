"""运行样例：构建、合并、分位数界限、跨边界并集与调和。"""

from fractions import Fraction

from constraints import reconcile, union
from histogram import BoundaryMismatch, FixedHistogram, IncompatibleHistograms
from table import Batch, Column, ColumnType, histogram_from_batch


def show(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


# 1. 带列类型的批次输入；null 与 NaN 分别计数
show("1. 类型化批次输入 -> 直方图（null 与 NaN 不同）")
nan = float("nan")
cols = [Column("latency", ColumnType.FLOAT), Column("tag", ColumnType.TEXT)]
batch1 = Batch(cols, [(1.5, "a"), (None, "b"), (7.0, "a"), (nan, "c")])
batch2 = Batch(cols, [(12.0, "a"), (3.0, "b"), (None, "c"), (25.0, "a")])
bounds = [0.0, 5.0, 10.0, 20.0]
h1 = histogram_from_batch(batch1, "latency", bounds)
h2 = histogram_from_batch(batch2, "latency", bounds)
print("批次1:", h1)
print("批次2:", h2)

# 2. 同边界合并 == 直接处理合并样本
show("2. 同边界合并：merge(h1,h2) == 直接处理合并样本")
merged = h1.merge(h2)
direct = histogram_from_batch(
    Batch(cols, list(batch1.rows) + list(batch2.rows)), "latency", bounds)
print("合并结果:", merged)
print("与直接处理一致:", merged == direct)

# 3. 分位数返回值区间与误差说明
show("3. 分位数：值区间 + 误差说明（不伪称精确值）")
qi = merged.quantile(0.5)
print("median:", qi)
print("说明:", qi.explanation)

# 4. 不同边界不可直接合并
show("4. 不同边界不可直接 merge")
try:
    h1.merge(FixedHistogram([0.0, 3.0, 20.0]))
except BoundaryMismatch as e:
    print("拒绝:", e)

# 5. 跨边界并集：约束表达未知拆分，分位数可靠界限 + 端点见证
show("5. 不同样本、不同边界的并集（union）")
s1 = FixedHistogram([0, 10, 20], counts=[6, 4])
s2 = FixedHistogram([0, 5, 15, 20], counts=[2, 6, 2])
print("分片1:", s1)
print("分片2:", s2)
u = union([s1, s2])
print("原子区间:", [str(a) for a in u.atoms])
qb = u.quantile(0.5)
print("median 界限:", qb)
print("说明:", qb.explanation)
lo_sample = u.expand_witness(qb.low_witness, "left")
hi_sample = u.expand_witness(qb.high_witness, "right")
print("低端点见证样本:", lo_sample, "-> 第", qb.rank, "个值 =",
      sorted(lo_sample)[qb.rank - 1])
print("高端点见证样本:", hi_sample, "-> 第", qb.rank, "个值 =",
      sorted(hi_sample)[qb.rank - 1])

# 6. 同样本调和：不相容必须拒绝
show("6. 同一样本、不同边界的调和（reconcile）")
sample = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
ha = FixedHistogram.from_values(sample, [0, 5, 10])
hb = FixedHistogram.from_values(sample, [0, 3, 7, 10])
ch = reconcile([ha, hb])
print("调和后原子:", [str(a) for a in ch.atoms])
print("median 界限:", ch.quantile(Fraction(1, 2)))
bad = FixedHistogram([0, 7, 10], counts=[0, 10])  # 与 ha 矛盾
try:
    reconcile([ha, bad])
except IncompatibleHistograms as e:
    print("拒绝:", e)
