"""DynamicConvexHull 运行样例。"""

from fractions import Fraction

from convex_hull import DynamicConvexHull


def show_hull(h, label):
    print(f"\n== {label} ==")
    for v in h.hull():
        print(f"  ({v.x}, {v.y})  <- ids={list(v.ids)}")


h = DynamicConvexHull()
# 同坐标挂多个 ID；包含有理数坐标与边上共线点
h.add("sensor-A", 0, 0)
h.add("sensor-B", 0, 0)                 # 与 A 同坐标
h.add("edge-mid", 2, 0)                 # 底边共线点，按策略保留
h.add("east", 4, 0)
h.add("north", 2, 3)
h.add("inner", Fraction(3, 2), Fraction(1, 2))  # 内部点，不改变包络
show_hull(h, "初始凸包（共线点保留，内部点被排除）")

r = h.extreme(1, 0)
print(f"\n方向 (1,0) 极值: value={r.value}, ids={[list(v.ids) for v in r.vertices]}")

h.add("ne", 4, 3)
r = h.extreme(1, 1)
print(f"加入 (4,3) 后方向 (1,1) 极值: value={r.value}, ids={[list(v.ids) for v in r.vertices]}")

# 删除同坐标的一个 ID：几何点不消失
h.remove("sensor-A")
print(f"\n删除 sensor-A 后 (0,0) 处存活 ID: {list(h.ids_at(0, 0))}")
show_hull(h, "删除 sensor-A 后（(0,0) 仍在包络上）")

# 删到全共线：退化为有序链
g = DynamicConvexHull()
for i, x in enumerate([3, 0, 2, 1]):
    g.add(i, x, 2 * x)
show_hull(g, "全共线点集 -> 有序链")
