"""polygon_clip 运行样例：python3.11 demo.py"""

from fractions import Fraction as F

from polygon_clip import clip_polygon, polygon_area


def fmt(value):
    if isinstance(value, F):
        return str(value.numerator) if value.denominator == 1 else f"{value.numerator}/{value.denominator}"
    return str(value)


def fmt_points(vertices):
    return "[" + ", ".join(f"({fmt(x)}, {fmt(y)})" for x, y in vertices) + "]"


def show(title, subject, window):
    print(f"=== {title} ===")
    print(f"被裁多边形: {subject}")
    print(f"裁剪窗:     {window}")
    try:
        result = clip_polygon(subject, window)
    except (ValueError, TypeError) as exc:
        print(f"拒绝输入: {type(exc).__name__}: {exc}\n")
        return
    print(f"原面积: {polygon_area(subject)}   裁后总面积: {result.total_area}")
    if not result.regions:
        print("结果: 空（无交集）\n")
        return
    for k, region in enumerate(result.regions, 1):
        kind = "退化(线/点)" if region.degenerate else "实体区域"
        print(f"  区域 {k} [{kind}] 面积={region.area}")
        print(f"    顶点:   {fmt_points(region.vertices)}")
        print(f"    边来源: {region.edge_sources}")
    print()


def main():
    show(
        "例1 正方形与正方形部分重叠（含来源边信息）",
        [(0, 0), (4, 0), (4, 4), (0, 4)],
        [(1, 1), (5, 1), (5, 5), (1, 5)],
    )
    show(
        "例2 拱门被裁成两个连通区域",
        [(0, 0), (10, 0), (10, 4), (8, 4), (8, 2), (2, 2), (2, 4), (0, 4)],
        [(0, 3), (10, 3), (10, 8), (0, 8)],
    )
    show(
        "例3 有理数坐标（精确 2/3，无 epsilon）",
        [(0, 0), (1, 0), (0, 1)],
        [(0, 0), (F(2, 3), 0), (F(2, 3), 1), (0, 1)],
    )
    show(
        "例4 边恰贴裁剪边界：边界不丢失",
        [(0, 0), (4, 0), (2, 3)],
        [(0, 0), (10, 0), (10, 10), (0, 10)],
    )
    show(
        "例5 两个实体矩形 + 一条贴边细线（退化区域被标记）",
        [(2, 0), (4, 0), (4, 3), (6, 3), (6, -2), (1, -2), (1, 3), (2, 3)],
        [(0, 0), (10, 0), (10, 10), (0, 10)],
    )
    show(
        "例6 仅顶点触碰窗口：退化为点",
        [(2, -2), (6, -2), (4, 0)],
        [(0, 0), (10, 0), (10, 10), (0, 10)],
    )
    show(
        "例7 非法输入：蝴蝶结（自交）被拒绝",
        [(0, 0), (2, 2), (2, 0), (0, 2)],
        [(0, 0), (10, 0), (10, 10), (0, 10)],
    )
    show(
        "例8 幂等性验证：对裁后区域再裁一次",
        [(0, 0), (7, 0), (7, 3), (5, 3), (5, 5), (2, 5), (2, 3), (0, 3)],
        [(1, 1), (6, 1), (6, 6), (1, 6)],
    )
    first = clip_polygon(
        [(0, 0), (7, 0), (7, 3), (5, 3), (5, 5), (2, 5), (2, 3), (0, 3)],
        [(1, 1), (6, 1), (6, 6), (1, 6)],
    )
    window = [(1, 1), (6, 1), (6, 6), (1, 6)]
    ok = True
    for region in first.solid:
        again = clip_polygon(region.vertices, window)
        ok &= [r.vertices for r in again.solid] == [region.vertices]
    print(f"  幂等性: 再次裁剪后几何不变 -> {ok}\n")


if __name__ == "__main__":
    main()
