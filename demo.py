"""Runnable sample for the exact 2D affine scene graph.

Run with:  python3.11 demo.py
"""

from fractions import Fraction

from affine2d import quarter_turn, rotation, scaling, translation
from scene import HitStatus, Scene, UnsolvableReparentError


def show(label, value):
    print(f"  {label:<38} {value}")


def main():
    scene = Scene()

    # 1. Build a small hierarchy: root -> arm -> tip.
    root = scene.create_node("root", local=translation(10, 0))
    arm = scene.create_node("arm", parent=root,
                            local=translation(1, 0) @ quarter_turn(1))
    tip = scene.create_node("tip", parent=arm,
                            local=scaling(2) @ translation(0, 3))
    print("1. Hierarchy root -> arm -> tip")
    show("world(tip) =", scene.world_matrix(tip))
    show("local (1,1) -> world =", scene.local_to_world(tip, 1, 1))

    # 2. Inverse hit test: world point -> tip-local coordinates.
    world_pt = scene.local_to_world(tip, 1, 1)
    hit = scene.hit(tip, *world_pt)
    print("2. Inverse hit test")
    show(f"hit(tip, {world_pt}) =", f"{hit.status.name} {hit.point}")

    # 3. Changing a parent transform moves the child's hits in sync.
    scene.set_local(root, translation(100, 0))
    print("3. After moving root to translation(100, 0)")
    show("hit at same world point =", scene.hit(tip, *world_pt).point)
    show("local (1,1) now at world =", scene.local_to_world(tip, 1, 1))

    # 4. Degenerate case: scale by 0 is singular, no pseudo-inverse.
    flat = scene.create_node("flat", local=scaling(0, 3))
    result = scene.hit(flat, 5, 6)
    print("4. Singular node (scale x by 0)")
    show("hit status =", f"{result.status.name} (point={result.point})")

    # 5. Atomic reparent with keep-world; singular parent is rejected.
    dock = scene.create_node("dock", local=translation(-4, 7) @ rotation(Fraction(3, 5), Fraction(4, 5)))
    before = scene.world_matrix(tip)
    scene.reparent(tip, dock, keep_world=True)
    print("5. Reparent tip under dock (keep_world=True)")
    show("world(tip) unchanged =", scene.world_matrix(tip) == before)
    try:
        scene.reparent(tip, flat, keep_world=True)
    except UnsolvableReparentError as exc:
        show("reparent under singular parent =", f"rejected: {exc}")

    # 6. Snapshots: query hits at old versions (multi-view).
    snap_now = scene.snapshot()
    probe = scene.local_to_world(tip, 2, -3)
    scene.set_local(root, translation(-50, 25))
    scene.set_local(dock, scaling(3))
    print("6. Snapshot taken before further edits")
    show("snapshot version =", snap_now.version)
    show("current version =", scene.version)
    show("hit at snapshot version =", snap_now.hit(tip, *probe).point)
    show("hit at current version =", scene.hit(tip, *probe).point)

    # 7. Cache statistics (ancestor-version-bound invalidation).
    print("7. Cache statistics")
    show("hits / misses =", f"{scene.cache_hits} / {scene.cache_misses}")


if __name__ == "__main__":
    main()
