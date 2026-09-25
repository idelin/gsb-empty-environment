"""Runnable demo: grid path planning with exact radius clearance.

Run with:  python3.11 demo.py
"""

from fractions import Fraction

from planner import GridPlanner


def fmt_pt(p):
    return f"({float(p[0]):6.2f},{float(p[1]):6.2f})"


def show(title, planner, result):
    print(f"== {title} ==")
    if result is None:
        print("  无可行路径 (no feasible path)\n")
        return
    print(f"  路径航点 ({len(result.path)} 个, 简化前 {len(result.raw_path)} 个):")
    for p in result.path:
        print(f"    {fmt_pt(p)}   exact=({p[0]}, {p[1]})")
    print(f"  每段碰撞校验 (机器人半径 r={planner.radius}, 要求 dist^2 >= {planner.r2}):")
    for s in result.segments:
        if s.min_dist2 is None:
            dist_info = "no obstacles"
        else:
            dist_info = f"min dist^2={s.min_dist2} (~{s.min_dist:.3f})"
        status = "PASS" if s.clear else "FAIL"
        print(f"    seg {s.index}: {fmt_pt(s.start)} -> {fmt_pt(s.end)}"
              f"  {dist_info}  [{status}]")
    print(f"  整体验证: {'全部通过' if result.ok else '存在碰撞段!'}\n")


def main():
    # 场景 1: 两个箱型障碍, 半径 1 的机器人绕行
    obstacles = [
        [(4, 4), (8, 4), (8, 8), (4, 8)],
        [(12, 2), (16, 2), (16, 6), (12, 6)],
    ]
    planner = GridPlanner(obstacles, radius=1, cell_size=1,
                          bounds=(0, 0, 20, 12))
    show("场景1: 双箱障碍绕行 (r=1)", planner, planner.plan((1, 1), (19, 10)))

    # 场景 2: 两个对角障碍夹缝 sqrt(2) < 2r -> 不可穿过
    walls = [
        [(-12, 1), (0, 1), (0, 3), (-12, 3)],   # 左墙, 右端角 (0,1)
        [(1, -2), (12, -2), (12, 0), (1, 0)],   # 右墙, 左端角 (1,0)
    ]
    planner = GridPlanner(walls, radius=1, cell_size=1, bounds=(-10, -6, 10, 6))
    show("场景2: 对角夹缝 sqrt(2) < 2r, 应无解", planner,
         planner.plan((-5, 4), (5, -4)))

    # 场景 3: 同样的夹缝加宽到 sqrt(10) > 2r -> 可以穿过
    walls_wide = [
        [(-12, 1), (0, 1), (0, 3), (-12, 3)],
        [(3, -2), (12, -2), (12, 0), (3, 0)],
    ]
    planner = GridPlanner(walls_wide, radius=1, cell_size=1,
                          bounds=(-10, -6, 10, 6))
    show("场景3: 对角夹缝 sqrt(10) > 2r, 应穿过", planner,
         planner.plan((-5, 4), (5, -4)))

    # 场景 4: 起点被膨胀障碍覆盖 -> 无解
    box = [[(0, 0), (6, 0), (6, 6), (0, 6)]]
    planner = GridPlanner(box, radius=1, cell_size=1)
    show("场景4: 起点在膨胀障碍内, 应无解", planner,
         planner.plan((3, 3), (10, 3)))

    # 场景 5: 有理数坐标 + 退化障碍(点、线段)
    shapes = [
        [(Fraction(5, 2), Fraction(-1, 2))],                    # 点障碍
        [(Fraction(1, 2), 1), (Fraction(7, 2), 1)],             # 线段障碍
    ]
    planner = GridPlanner(shapes, radius=Fraction(1, 2),
                          cell_size=Fraction(1, 2))
    show("场景5: 有理数坐标 + 点/线段退化障碍 (r=1/2)", planner,
         planner.plan((0, 0), (5, 2)))


if __name__ == "__main__":
    main()
