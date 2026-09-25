"""运行样例：诊断 -> 修复建议 -> 手动应用补丁 -> 事务编辑 -> 失败回滚。"""
from fractions import Fraction

from mesh_topology import Mesh, analyze, suggest_repairs


def show(title):
    print("\n" + "=" * 60)
    print(title)
    print("=" * 60)


# 一个“问题网格”：包含方向冲突、非流形边、重复面、退化面、孤立顶点、蝴蝶结顶点
coords = [
    (0, 0, 0), (1, 0, 0), (0, 1, 0), (0, 0, 1),   # 0-3  四面体
    (1, 1, 0),                                      # 4    非流形边用
    (2, 2, 2), (3, 3, 3),                           # 5,6  退化面（共线）
    (9, 9, 9),                                      # 7    孤立顶点
    (5, 0, 0), (6, 0, 0), (5, 1, 0),                # 8-10 蝴蝶结：扇 1
    (4, 0, 0), (5, -1, 0),                          # 11,12 蝴蝶结：扇 2（共享顶点 8 的坐标? 见下）
]
faces = [
    (0, 2, 1), (0, 1, 3), (1, 2, 3), (2, 0, 3),   # 0-3  闭合四面体
    (0, 1, 4),                                    # 4    使边 (0,1) 挂 3 个面 -> 非流形
    (0, 2, 1),                                    # 5    与面 0 重复
    (5, 6, 0),                                    # 6    退化：5,6 共线于原点方向? 见说明
    (8, 9, 10), (8, 11, 12),                      # 7,8  顶点 8 的蝴蝶结（两个扇仅共享顶点 8）
]

show("1. 从三角面列表建半边结构并诊断")
mesh = Mesh(coords, faces)
# 面 6 的顶点 5,6 与原点共线 -> 精确零面积；顶点 7 孤立
print(analyze(mesh).summary())

show("2. 生成修复建议（独立补丁 + 前置条件，不自动应用）")
patches = suggest_repairs(mesh)
for i, p in enumerate(patches):
    unmet = p.preconditions(mesh)
    status = "可应用" if not unmet else f"前置条件未满足: {unmet}"
    print(f"  [{i}] ({p.kind}) {p.description}  -- {status}")
print(f"网格未变: 面数仍为 {len(mesh.faces)}")

show("3. 手动应用选定补丁（用户决策，绝不自动应用）")
# 决策：删重复面、删退化面、边 (0,1) 上删多余的面 4、移除孤立顶点
chosen = [p for p in patches if p.kind in ("delete_duplicate", "remove_isolated_vertex")
          or (p.kind == "delete_face" and p.params["face"] in (4, 6))]
for p in chosen:
    p.apply(mesh)
    print(f"  已应用: {p.description}")
print(f"一致性检查: {mesh.check_consistency() or '通过'}")
diag = analyze(mesh)
print(f"  剩余非流形边: {diag.nonmanifold_edges}")
print(f"  剩余蝴蝶结顶点: {[v for v, _ in diag.bowtie_vertices]}")

show("4. 蝴蝶结顶点拆分补丁（注意：移除孤立顶点后索引已重排）")
for p in suggest_repairs(mesh):
    if p.kind == "split_vertex":
        p.apply(mesh)
        print(f"  已应用: {p.description}")
diag = analyze(mesh)
print(f"  剩余蝴蝶结顶点: {diag.bowtie_vertices}")
print(f"  剩余非流形边: {diag.nonmanifold_edges}")
print(f"  删面后新产生的孤立顶点: {diag.isolated_vertices}")
while True:  # 每轮重新诊断；过期补丁会被前置条件安全拦截
    stale = [p for p in suggest_repairs(mesh) if p.kind == "remove_isolated_vertex"]
    if not stale:
        break
    for p in stale:
        try:
            p.apply(mesh)
            print(f"  已应用: {p.description}")
        except RuntimeError as e:
            print(f"  前置条件拦截（索引已重排，补丁过期）: {e}")
print(f"  最终孤立顶点: {analyze(mesh).isolated_vertices}")

show("5. 事务编辑：边拆分 + 面插删")
with mesh.transaction() as tx:
    m = tx.split_edge(0, 3)  # 边 (0,0,0)-(0,0,1)，精确中点
    print(f"  拆分边 (0,3)，新顶点 {m}，坐标 {mesh.coords[m]}")
    assert mesh.coords[m] == (0, 0, Fraction(1, 2))
    tx.insert_face(0, 1, 2)
    tx.delete_face(1)
print(f"  事务提交，一致性检查: {mesh.check_consistency() or '通过'}")

show("6. 失败事务：原拓扑与旧快照完整保留")
snap = (list(mesh.coords), list(mesh.faces))
try:
    with mesh.transaction() as tx:
        tx.split_edge(0, 1)
        tx.delete_face(0)
        raise RuntimeError("模拟中途失败")
except RuntimeError as e:
    print(f"  捕获异常: {e}")
print(f"  拓扑已恢复: {(mesh.coords, mesh.faces) == snap}")
print(f"  一致性检查: {mesh.check_consistency() or '通过'}")
