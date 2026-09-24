"""Runnable demo: 1D bar with 3 linear elements, 4 nodes.

Shows shared-node assembly, provenance, element correction, retraction,
Dirichlet elimination with rhs correction, and constraint re-versioning.
Run: python3.11 example.py
"""

from assembly import Assembler, assemble_reference, contribution
from dense import solve_dense

KE = [[1.0, -1.0], [-1.0, 1.0]]   # unit stiffness, unit length
FE = [0.5, 0.5]                   # unit uniform load


def show(title, asm):
    print(f"--- {title} (constraint version {asm.constraint_version}) ---")
    for row in asm.matrix:
        print("  A |", " ".join(f"{v:7.3f}" for v in row))
    print("  b |", " ".join(f"{v:7.3f}" for v in asm.rhs))


def main():
    asm = Assembler(size=4)
    elements = [
        contribution("e1", [0, 1], KE, FE),
        contribution("e2", [1, 2], KE, FE),
        contribution("e3", [2, 3], KE, FE),
    ]
    asm.add_elements(elements)
    show("assembled, unconstrained", asm)

    print("provenance A[1][1]:", asm.entry_provenance(1, 1))
    print("provenance b[2]   :", asm.rhs_provenance(2))
    print()

    asm.set_constraints({0: 0.0, 3: 0.5})
    show("constraints u0=0, u3=0.5 (linear elimination)", asm)
    u = solve_dense(asm.matrix, asm.rhs)
    print("  solution u =", [round(v, 6) for v in u])
    print("  rhs provenance b[2]:", asm.rhs_provenance(2))
    print()

    # Correct element e2 (stiffer) without touching e1/e3, then retract e3.
    asm.update_element("e2", [1, 2], [[2.0, -2.0], [-2.0, 2.0]], FE)
    show("element e2 corrected (k=2)", asm)
    asm.remove_element("e3")
    show("element e3 retracted", asm)

    # Constraint change: undo old elimination, re-apply incrementally.
    asm.set_constraints({0: 0.0, 3: 1.0})
    show("constraints changed to u0=0, u3=1.0", asm)

    # Cross-check the incremental result against a full re-assembly.
    remaining = [
        contribution("e1", [0, 1], KE, FE),
        contribution("e2", [1, 2], [[2.0, -2.0], [-2.0, 2.0]], FE),
    ]
    ref_a, ref_b = assemble_reference(4, remaining, {0: 0.0, 3: 1.0})
    ok_a = all(abs(asm.matrix[i][j] - ref_a[i][j]) < 1e-12
               for i in range(4) for j in range(4))
    ok_b = all(abs(asm.rhs[i] - ref_b[i]) < 1e-12 for i in range(4))
    print(f"matches independent full assembly: matrix={ok_a}, rhs={ok_b}")

    # Invalid mapping -> the whole batch is rejected.
    try:
        asm.add_elements([contribution("e4", [1, 2], KE, FE),
                          contribution("e5", [2, 99], KE, FE)])
    except Exception as exc:
        print(f"invalid batch rejected: {exc}")
        print("elements still:", asm.element_ids())


if __name__ == "__main__":
    main()
