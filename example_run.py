from solution_enumerator import ProjectingEnumerator

variables = {"x": tuple(range(3)), "a": tuple(range(4))}
constraints = (lambda env: env["x"] + env["a"] <= 3,)

print("Project onto x; stop after a two-node budget:")
enumerator = ProjectingEnumerator(variables, constraints, projection=("x",))
first = enumerator.run(node_budget=2)
for solution in first.solutions:
    print(" ", solution.values, "witness", solution.witness)
print(" status:", first.status, "resumable:", first.resumable)
print(" stack:", enumerator.search_stack)

print("Resume:")
rest = enumerator.run()
for solution in rest.solutions:
    print(" ", solution.values, "witness", solution.witness)
print(" status:", rest.status, "complete:", rest.complete)

print("Empty projection:")
empty = ProjectingEnumerator(variables, constraints, projection=()).run()
print(" solutions:", [solution.values for solution in empty.solutions])
print(" complete:", empty.complete)
