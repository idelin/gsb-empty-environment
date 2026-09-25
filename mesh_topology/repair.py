"""局部修复建议：生成独立可执行的补丁，附带前置条件；绝不自动应用。"""
from __future__ import annotations

from dataclasses import dataclass, field

from .diagnostics import analyze, vertex_fan_components
from .mesh import Mesh


@dataclass
class RepairPatch:
    """一个独立可执行的修复补丁。

    - preconditions(mesh)：返回未满足的前置条件列表（空列表表示可应用）。
    - apply(mesh)：前置条件全部满足时在事务内执行；否则抛错，网格不变。
    """

    kind: str
    description: str
    params: dict = field(default_factory=dict)

    def preconditions(self, mesh: Mesh) -> list[str]:
        p = self.params
        unmet: list[str] = []
        live = mesh.live_faces()
        if self.kind in ("flip_face", "delete_face"):
            if p["face"] not in live:
                unmet.append(f"面 {p['face']} 不存在")
        elif self.kind == "delete_duplicate":
            fid, keeper = p["face"], p["keeper"]
            if fid not in live:
                unmet.append(f"面 {fid} 不存在")
            if keeper not in live:
                unmet.append(f"保留面 {keeper} 不存在")
            if fid in live and keeper in live:
                if fid == keeper:
                    unmet.append("删除面与保留面相同")
                elif sorted(live[fid]) != sorted(live[keeper]):
                    unmet.append(f"面 {fid} 与面 {keeper} 顶点集不再相同")
        elif self.kind == "split_vertex":
            vid, faces = p["vertex"], tuple(p["faces"])
            if not (0 <= vid < len(mesh.coords)):
                unmet.append(f"顶点 {vid} 不存在")
            else:
                comps = vertex_fan_components(mesh, vid)
                if len(comps) < 2:
                    unmet.append(f"顶点 {vid} 的面扇已连通，无需拆分")
                elif faces not in comps:
                    unmet.append(f"面片组 {list(faces)} 不再是顶点 {vid} 的独立扇组件")
                for fid in faces:
                    if fid not in live:
                        unmet.append(f"面 {fid} 不存在")
                    elif vid not in live[fid]:
                        unmet.append(f"面 {fid} 不再引用顶点 {vid}")
        elif self.kind == "remove_isolated_vertex":
            vid = p["vertex"]
            if not (0 <= vid < len(mesh.coords)):
                unmet.append(f"顶点 {vid} 不存在")
            elif vid in mesh.vertex_faces():
                unmet.append(f"顶点 {vid} 已被面引用，不是孤立顶点")
        else:
            unmet.append(f"未知补丁类型 {self.kind!r}")
        return unmet

    def apply(self, mesh: Mesh) -> None:
        unmet = self.preconditions(mesh)
        if unmet:
            raise RuntimeError(
                f"补丁前置条件不满足，未应用: {'; '.join(unmet)}"
            )
        with mesh.transaction():
            self._apply(mesh)

    def _apply(self, mesh: Mesh) -> None:
        p = self.params
        if self.kind == "flip_face":
            mesh.flip_face(p["face"])
        elif self.kind in ("delete_face", "delete_duplicate"):
            mesh.delete_face(p["face"])
        elif self.kind == "remove_isolated_vertex":
            mesh.remove_isolated_vertex(p["vertex"])
        elif self.kind == "split_vertex":
            vid = p["vertex"]
            new_vid = mesh.add_vertex(mesh.coords[vid])
            for fid in p["faces"]:
                tri = mesh.faces[fid]
                mesh._replace_face(
                    fid, tuple(new_vid if v == vid else v for v in tri)
                )
        else:  # pragma: no cover - preconditions 已拦截
            raise ValueError(f"未知补丁类型 {self.kind!r}")


def suggest_repairs(mesh: Mesh) -> list[RepairPatch]:
    """根据当前诊断生成修复建议；只生成建议，绝不修改网格。"""
    diag = analyze(mesh)
    live = mesh.live_faces()
    patches: list[RepairPatch] = []

    for (a, b), f1, f2 in diag.orientation_conflicts:
        # 互为重复面的冲突由 delete_duplicate 处理，避免重复建议
        if sorted(live[f1]) == sorted(live[f2]):
            continue
        patches.append(RepairPatch(
            "flip_face",
            f"翻转面 {f2} 的绕向，消除边 ({a},{b}) 上的方向冲突",
            {"face": f2},
        ))

    for group in diag.duplicate_face_groups:
        keeper = group[0]
        for fid in group[1:]:
            patches.append(RepairPatch(
                "delete_duplicate",
                f"删除重复面 {fid}（保留面 {keeper}）",
                {"face": fid, "keeper": keeper},
            ))

    for fid, reason in diag.degenerate_faces:
        patches.append(RepairPatch(
            "delete_face",
            f"删除退化面 {fid}（{reason}）",
            {"face": fid},
        ))

    for (a, b), fids in diag.nonmanifold_edges:
        for fid in fids:
            patches.append(RepairPatch(
                "delete_face",
                f"删除面 {fid}：边 ({a},{b}) 连接 {len(fids)} 个面，"
                f"需删至 2 个（共 {len(fids)} 个备选，任选 {len(fids) - 2} 个删除）",
                {"face": fid},
            ))

    for vid, comps in diag.bowtie_vertices:
        by_size = sorted(comps, key=len, reverse=True)
        for comp in by_size[1:]:
            patches.append(RepairPatch(
                "split_vertex",
                f"把蝴蝶结顶点 {vid} 的面片组 {list(comp)} 拆分到新顶点（坐标不变）",
                {"vertex": vid, "faces": comp},
            ))

    for vid in diag.isolated_vertices:
        patches.append(RepairPatch(
            "remove_isolated_vertex",
            f"移除孤立顶点 {vid}（其余顶点索引将重排）",
            {"vertex": vid},
        ))

    return patches
