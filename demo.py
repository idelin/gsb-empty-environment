from impact_graph import ImpactGraph


def show(title: str, graph: ImpactGraph, vertex: str | None = None) -> None:
    print(title)
    print("affected:", ", ".join(graph.affected_vertices()) or "(empty)")
    if vertex is not None:
        explanation = graph.explain(vertex)
        if explanation is None:
            print(f"{vertex}: no evidence")
        else:
            chain = " -> ".join(explanation.chain)
            edges = ", ".join(explanation.edges)
            facts = ", ".join(explanation.facts) or "(none)"
            print(f"{vertex}: {chain}")
            print(f"edges   : {edges}")
            print(f"facts   : {facts}")
    print()


graph = ImpactGraph()
for vertex in ("root", "compile", "test", "release", "standby", "isolated"):
    graph.add_vertex(vertex)

graph.add_edge("compile-on-merge", "root", "compile", ("not", "rollback"))
graph.add_edge("test-normal", "compile", "test", "gate-open")
graph.add_edge("test-hotfix-window", "standby", "test", start=10, end=20)
graph.add_edge("release-after-test", "test", "release", "signed")

graph.add_change("root")
graph.add_change("standby")
show("1. Initial graph: isolated vertices stay, gate is closed", graph, "release")

graph.update_facts(add=("gate-open", "signed"))
show("2. Facts make compile -> test -> release available", graph, "release")

graph.update_facts(remove=("signed",))
show("3. Revoke 'signed': release loses all evidence", graph, "release")

graph.update_facts(add=("signed",))
graph.update_facts(add=("rollback",))
show("4. Revoke compile edge fact: short chain disappears", graph, "test")

graph.set_time(10)
show("5. t=10 is included by half-open [10, 20): hotfix evidence exists", graph, "test")

graph.set_time(20)
show("6. t=20 is excluded by [10, 20): hotfix evidence disappears", graph, "test")

graph.update_facts(remove=("rollback",), add=("gate-open", "signed"))
graph.set_time(0)
show("7. Main evidence restored", graph, "release")
