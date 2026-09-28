from fdcsp import Assumption, Solver, validate_assignment


def main() -> None:
    solver = Solver({"x": {1, 2, 3}, "y": {1, 2, 3}, "z": {1, 2, 3}})
    solver.add_constraint(["x"], lambda x: x != 1, "x is not 1")
    solver.add_constraint(["x", "y"], lambda x, y: x != y, "x != y")
    solver.add_constraint(["y", "z"], lambda y, z: y != z, "y != z")
    solver.add_constraint(["x", "z"], lambda x, z: x != z, "x != z")

    print("base propagation:", solver.propagate(), solver.domains())
    print("base removals:", solver.domains_store.removals_by_level())

    with solver.assuming((("y", 3),)):
        print("outer scope depth:", solver.scope_depth)
        first = solver.solve()
        print("solution under y=3:", first)
        print("domains after solve are not mutated:", solver.domains())
        print("outer propagation:", solver.propagate(), solver.domains())
        print("outer removals:", solver.domains_store.removals_by_level())

        with solver.assume("x", 3):
            print("inner scope depth:", solver.scope_depth)
            print("conflicting x=3 propagation:", solver.propagate())

        print("after inner cancellation, outer solves:", solver.solve())

    print("after outer cancellation:", solver.domains())

    with solver.assume("x", 2):
        with solver.assume("x", 2):
            print("repeated equal assumptions retain nested depth:", solver.scope_depth)
            print("solution through repeated assumptions:", solver.solve())

    valid = {"x": 2, "y": 3, "z": 1}
    invalid = {"x": 2, "y": 2, "z": 1}
    print("independent checker valid:", validate_assignment(
        valid, solver.initial_domains(), solver.constraints()
    ))
    print("independent checker invalid:", validate_assignment(
        invalid, solver.initial_domains(), solver.constraints()
    ))
    print("independent checker with active assumption:", validate_assignment(
        valid, solver.initial_domains(), solver.constraints(), [Assumption("x", 2)]
    ))


if __name__ == "__main__":
    main()
