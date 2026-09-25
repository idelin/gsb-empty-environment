"""Runnable sample: hierarchy, cached worlds, inverse hits, reparenting,
singular handling and old-version snapshot queries."""

from fractions import Fraction

from affine2d import Matrix, Scene, SingularTransformError


def show(title, value):
    print(f"  {title:<28} {value}")


def main():
    scene = Scene()
    root = scene.create_node("root", local=Matrix.translation(10, 0))
    arm = scene.create_node("arm", parent=root,
                            local=Matrix.rotation_quarter(1))  # 90 deg CCW
    tip = scene.create_node("tip", parent=arm,
                            local=Matrix.translation(0, 5))

    print("== world matrices (cached, ancestor-version dependent) ==")
    show("world(root)", scene.world_matrix(root))
    show("world(arm)", scene.world_matrix(arm))
    show("world(tip)", scene.world_matrix(tip))

    print("\n== inverse hit: world point -> tip-local ==")
    hit = scene.hit(tip, (10, 15))
    show("hit(tip, (10,15))", hit.point)
    show("round-trip check", scene.world_matrix(tip).apply(hit.point))

    print("\n== order matters: translate-then-rotate != rotate-then-translate ==")
    t, r = Matrix.translation(10, 0), Matrix.rotation_quarter(1)
    show("(T @ R) @ (1,0)", (t @ r).apply((1, 0)))
    show("(R @ T) @ (1,0)", (r @ t).apply((1, 0)))

    print("\n== parent edit moves child hits (cache invalidated) ==")
    before = scene.hit(tip, (10, 15)).point
    scene.set_local(root, Matrix.translation(100, 0))
    after = scene.hit(tip, (10, 15)).point
    show("tip-local before", before)
    show("tip-local after", after)
    show("cache valid for tip?", scene.is_cache_valid(tip) or "invalidated -> recomputed")

    print("\n== snapshot: old-version hit queries ==")
    snap = scene.snapshot()
    scene.set_local(root, Matrix.translation(-50, 20))
    show("snapshot hit(tip,(10,15))", snap.hit(tip, (10, 15)).point)
    show("live     hit(tip,(10,15))", scene.hit(tip, (10, 15)).point)

    print("\n== atomic reparent with keep_world ==")
    other = scene.create_node("other", local=Matrix.scale(2))
    world_before = scene.world_matrix(tip)
    scene.reparent(arm, other, keep_world=True)
    show("world(tip) preserved", scene.world_matrix(tip) == world_before)

    print("\n== singular cases: explicit status, no pseudo-inverse ==")
    flat = scene.create_node("flat", local=Matrix.scale(0))
    show("scale(0).inverse()", Matrix.scale(0).inverse())
    show("hit(flat, (1,1))", scene.hit(flat, (1, 1)))
    try:
        scene.reparent(tip, flat, keep_world=True)
    except SingularTransformError as exc:
        show("keep_world -> singular", f"rejected: {exc}")
    show("tip still under arm?", tip.parent.name)

    print("\n== exact rational coordinates (no epsilon anywhere) ==")
    m = Matrix(Fraction(2, 3), 0, 0, Fraction(1, 5), 7, -3)
    n = scene.create_node("rat", local=m)
    p = (Fraction(11, 7), Fraction(-2, 9))
    show("world of rational point", scene.world_matrix(n).apply(p))
    show("inverse hit round-trips", scene.hit(n, scene.world_matrix(n).apply(p)).point == p)


if __name__ == "__main__":
    main()
