"""运行样例：python3 main.py"""
from planar_graph import Segment, build_planar_graph


def fmt(p):
    return f"({p[0]}, {p[1]})"


def main():
    segments = [
        Segment.of(0, 0, 4, 4, "diag"),          # 与 cross 十字相交
        Segment.of(0, 4, 4, 0, "cross"),
        Segment.of(0, 0, 8, 0, "base-a"),        # 与 base-b 部分重叠 [2,0]-[6,0]
        Segment.of(2, 0, 6, 0, "base-b"),
        Segment.of(4, 4, 8, 4, "dup-1"),         # 完全重合，双来源
        Segment.of(4, 4, 8, 4, "dup-2"),
        Segment.of("1/2", "1/2", "3/2", "3/2", "frac"),  # 有理数，落在 diag 上
        Segment.of(6, 0, 6, 3, "tee"),           # T 型：端点 (6,0) 在 base-a/base-b 上
        Segment.of(5, 5, 5, 5, "zero-len"),      # 零长度，孤立点
    ]
    g = build_planar_graph(segments)

    print(f"顶点数: {len(g.vertices)}")
    for i, v in enumerate(g.vertices):
        print(f"  v{i}: {fmt(v)}")
    print(f"子边数: {len(g.edges)}")
    for e in g.edges:
        print(f"  v{e.v1} {fmt(g.vertices[e.v1])} -- v{e.v2} {fmt(g.vertices[e.v2])}"
              f"  来源={list(e.sources)}")
    print(f"孤立点(零长度输入): {len(g.isolated_points)}")
    for ip in g.isolated_points:
        print(f"  {fmt(ip.at)} 标签={ip.label!r} 落在线段={list(ip.on_segments)}")


if __name__ == "__main__":
    main()
