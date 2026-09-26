"""分段有理表达式：求和、乘法与连续性检查 —— 运行样例。

运行：python3 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from rational_pieces import (
    CombinedPiecewise, Interval, IsolationBudget, Piece, Piecewise, Poly,
    RationalFunction,
)


def banner(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


x = Poly.x()

# ------------------------------------------------------------ 1. 加法与共同细分
banner("1. 分段加法：自动细分交叠区间，保留来源映射")
f = Piecewise([
    Piece('f1', Interval(0, 1, True, True), RationalFunction.of(Poly([1]), label='1')),
    Piece('f2', Interval(1, 2, False, True), RationalFunction.of(x, label='x')),
])
g = Piecewise.single(Interval(0, 2, True, True),
                     RationalFunction.of(Poly([1, 1]), label='x+1'), pid='g1')
h = CombinedPiecewise(f, g, 'add')
print("f = 1 (on [0,1]), x (on (1,2]) ;  g = x+1 (on [0,2])")
print("h = f + g 的共同细分：")
for s in h.segments:
    print("  ", s)
print("\n连续性报告：")
print(h.analyze())

# ------------------------------------------------------------ 2. 乘法与空洞保留
banner("2. 乘法：空洞（无定义区域）保持无定义")
u = Piecewise.single(Interval(0, 2, True, True),
                     RationalFunction.of(x, label='x'), pid='u1')
v = Piecewise.single(Interval(1, 3, True, True),
                     RationalFunction.of(Poly([-1, 1]), Poly([1]), label='x-1'), pid='v1')
w = CombinedPiecewise(u, v, 'mul')
print("u = x (on [0,2]) ; v = x-1 (on [1,3])")
print("w = u * v 的细分段（仅交叠区有定义）：")
for s in w.segments:
    print("  ", s)
print("区间 [0,1) 与 (2,3] 为空洞，保持无定义。")

# ------------------------------------------------------------ 3. 可去奇点与极点
banner("3. 定义域空洞、可去奇点与极点（精确代数证据）")
r1 = RationalFunction.of(Poly([-1, 0, 1]), Poly([-1, 1]), '(x^2-1)/(x-1)')
pw1 = Piecewise.single(Interval(0, 3, True, True), r1, pid='r1')
print("片段 (x^2-1)/(x-1) on [0,3]：")
print(pw1.analyze())
print("\n变换记录（每步注明适用域）：")
for t in r1.trace:
    print("  *", t)

r2 = RationalFunction.of(Poly([1]), Poly([-2, 1]), '1/(x-2)')
pw2 = Piecewise.single(Interval(0, 4, True, True), r2, pid='r2')
print("\n片段 1/(x-2) on [0,4]：")
print(pw2.analyze())

# ------------------------------------------------------------ 4. 跳变与单侧极限
banner("4. 边界左右值不同：报告跳变（精确值）")
j = Piecewise([
    Piece('j1', Interval(0, 1, True, True),
          RationalFunction.of(Poly([0, 2]), label='2x')),
    Piece('j2', Interval(1, 2, False, True),
          RationalFunction.of(Poly([3]), label='3')),
])
print("j = 2x (on [0,1]), 3 (on (1,2])：")
print(j.analyze())

# ------------------------------------------------------------ 5. 单侧定义不冒称连续
banner("5. 只一侧定义：不冒称连续")
one = Piecewise.single(Interval(0, 1, True, True),
                       RationalFunction.of(Poly([5]), label='5'), pid='o1')
print("片段 5 on [0,1]：")
print(one.analyze())

# ------------------------------------------------------------ 6. 跨排除点不合并
banner("6. 相同表达式跨排除点不合并")
base = Piecewise.single(Interval(0, 2, True, True),
                        RationalFunction.of(Poly([1]), label='1'), pid='b0')
same_open = Piecewise([
    Piece('s1', Interval(0, 1, True, False), RationalFunction.of(Poly([3]), label='3')),
    Piece('s2', Interval(1, 2, False, True), RationalFunction.of(Poly([3]), label='3')),
])
m = CombinedPiecewise(base, same_open, 'add')
print("1 + 3，右侧两片在 x=1 均为开端点（排除点）：")
for s in m.segments:
    print("  ", s)
print("-> 两段表达式相同但未合并（x=1 为排除点）")

# ------------------------------------------------------------ 7. 增量编辑
banner("7. 片段编辑后的增量维护（未受影响段对象复用）")
inc = CombinedPiecewise(f, g, 'add')
before = {id(s): s for s in inc.segments}
print("编辑前细分段：")
for s in inc.segments:
    print("  ", s)
inc.edit('L', 'f2', func=RationalFunction.of(Poly([2]), label='2'))
after = inc.segments
reused = [s for s in after if id(s) in before]
print("\n将 f2 改为常数 2 后：")
for s in after:
    print("  ", s)
print(f"\n复用未受影响段 {len(reused)}/{len(after)}（按对象同一性统计，未重算）")
print("编辑后连续性：")
print(inc.analyze())

# ------------------------------------------------------------ 8. 预算耗尽保留未决
banner("8. 根隔离预算耗尽：明确保留未决边界")
den = Poly([-2, 0, 1]) * Poly([-8, 0, 1])   # (x^2-2)(x^2-8)，根 ±√2, ±2√2
hard = RationalFunction.of(Poly([1]), den, '1/((x^2-2)(x^2-8))')
pwh = Piecewise.single(Interval(0, 3, True, True), hard, pid='hard')
print("片段 1/((x^2-2)(x^2-8)) on [0,3]，预算 max_steps=0：")
print(pwh.analyze(IsolationBudget(max_steps=0)))
print("\n同一函数给足预算 (max_steps=64)：")
print(pwh.analyze(IsolationBudget(max_steps=64)))
