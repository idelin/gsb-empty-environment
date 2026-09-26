"""Runnable sample for the tree quota allocator.

Covers all four statuses: FEASIBLE (budget binds), OPTIMAL (budget covers
every demand), BUDGET_UNDECIDED (no root bounds) and INFEASIBLE (conflicting
bounds, minimal conflicting subtree reported).
"""

from allocator import Node, Status, allocate, verify


def show(title, root):
    res = allocate(root)
    print(f"== {title} ==")
    print(f"status: {res.status.value}")
    if res.status is Status.INFEASIBLE:
        conflict = res.conflict
        print(f"minimal conflicting subtree rooted at: {conflict.node_id!r}")
        print(f"reason: {conflict.reason}")
        print(f"subtree: {conflict.subtree}")
    else:
        print("node totals:")
        for node_id in sorted(res.totals):
            print(f"  {node_id}: {res.totals[node_id]}")
        print("leaf allocations:")
        for leaf_id in sorted(res.allocations):
            print(f"  {leaf_id}: {res.allocations[leaf_id]}")
        errors = verify(root, res)
        print(f"conservation/bounds check: {'OK' if not errors else errors}")
    print()


def org_tree(upper, eng_upper=40):
    return Node.branch("hq", [
        Node.branch("eng", [
            Node.leaf("eng.backend", demand=30, weight=3),
            Node.leaf("eng.frontend", demand=20, weight=2),
        ], upper=eng_upper),
        Node.branch("ops", [
            Node.leaf("ops.infra", demand=25, weight=2),
            Node.leaf("ops.support", demand=15, weight=1),
        ], lower=10),
    ], upper=upper)


def main():
    show("FEASIBLE: budget 60 < total demand 90", org_tree(upper=60))
    show("OPTIMAL: budget 100 >= total demand 90 (no caps bind)",
         org_tree(upper=100, eng_upper=50))
    show("BUDGET_UNDECIDED: root carries no bounds",
         Node.branch("hq", [
             Node.leaf("team.a", demand=7, weight=1),
             Node.leaf("team.b", demand=5, weight=1),
         ]))
    show("INFEASIBLE: child lowers (8+8) exceed root upper 10",
         Node.branch("hq", [
             Node.branch("eng", [Node.leaf("eng.a", 20, 1)], lower=8),
             Node.branch("ops", [Node.leaf("ops.a", 20, 1)], lower=8),
         ], upper=10))
    show("FEASIBLE with rational data: upper 25/2 -> 12",
         Node.branch("hq", [
             Node.leaf("team.a", demand=10, weight="1/2"),
             Node.leaf("team.b", demand=10, weight="3/2"),
         ], upper="25/2"))


if __name__ == "__main__":
    main()
