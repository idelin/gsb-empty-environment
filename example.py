"""Run sample: 4-node spring chain with shared dofs, constraints, updates.

    node:   0 --bar0-- 1 --bar1-- 2 --bar2-- 3
    u0 = 0 (homogeneous),  u3 = 0.5 (non-homogeneous -> rhs correction)

Run with:  python3.11 example.py
"""

from assembler import Assembler, Constraint


def show_matrix(title, a):
    print(title)
    for row in a:
        print("   ", " ".join(f"{v:9.4f}" for v in row))


def show_vec(title, v):
    print(title, "[" + ", ".join(f"{x:9.4f}" for x in v) + "]")


def main():
    asm = Assembler(4)
    ke = [[1.0, -1.0], [-1.0, 1.0]]
    fe = [0.5, 0.5]
    asm.apply_batch([
        ("add", "bar0", [0, 1], ke, fe),
        ("add", "bar1", [1, 2], ke, fe),
        ("add", "bar2", [2, 3], ke, fe),
    ])

    show_matrix("global stiffness (shared dofs accumulate):", asm.matrix)
    show_vec("global load:", asm.rhs)

    print("\nprovenance of shared entry A[1][1] (bar0 + bar1):")
    for c in asm.provenance(1, 1):
        print(f"    element={c.source!r} local={c.local_index} value={c.value:+.1f}")

    asm.set_constraints([
        Constraint(dof=0, terms=(), constant=0.0),
        Constraint(dof=3, terms=(), constant=0.5),
    ])
    rs = asm.constrained_system()
    print(f"\nconstraint version {rs.version}, free dofs {rs.free_dofs}")
    show_matrix("reduced stiffness A_red = T^T A T:", rs.matrix)
    show_vec("reduced load b_red = T^T (b - A g):", rs.rhs)
    print("rhs provenance of b_red[1] (note the constraint correction term):")
    for c in rs.rhs_provenance[1]:
        print(f"    kind={c.kind:9s} source={c.source!r} value={c.value:+.4f} v{c.version}")
    show_vec("solution u:", asm.solve())

    print("\n-- update bar1 stiffness to 2.0 (incremental, touches only bar1) --")
    asm.reset_touch_count()
    asm.update_element("bar1", [1, 2], [[2.0, -2.0], [-2.0, 2.0]], fe)
    print(f"entries touched: {asm.touch_count} (independent of element count)")
    show_vec("solution u:", asm.solve())

    print("\n-- change constraint u3: 0.5 -> 1.0 (old assembly undone, rebuilt) --")
    asm.set_constraints([
        Constraint(dof=0, terms=(), constant=0.0),
        Constraint(dof=3, terms=(), constant=1.0),
    ])
    rs = asm.constrained_system()
    print(f"constraint version now {rs.version}")
    show_vec("solution u:", asm.solve())

    print("\n-- withdraw bar0, then batch with a bad mapping is rejected whole --")
    asm.remove_element("bar0")
    try:
        asm.apply_batch([
            ("add", "ok", [0, 1], ke, fe),
            ("add", "bad", [1, 9], ke, fe),   # dof 9 out of range
        ])
    except Exception as exc:
        print(f"batch rejected: {exc}")
    print("elements still present:", asm.element_ids())


if __name__ == "__main__":
    main()
