"""intprop 运行样例：覆盖需求中的各个典型场景。"""

from intprop import Solver, check_assignment


def show(title):
    print(f"\n=== {title} ===")


show("2x <= 3  =>  x <= 1")
s = Solver()
s.add_variable("x", -10, 10)
s.add_constraint([(2, "x")], 3)
print(f"x in [{s.lo['x']}, {s.hi['x']}]")
print("explain:", s.explain("x"))

show("-2x <= 3  =>  x >= -1（负系数向上取整）")
s = Solver()
s.add_variable("x", -10, 10)
s.add_constraint([(-2, "x")], 3)
print(f"x in [{s.lo['x']}, {s.hi['x']}]")
print("explain:", s.explain("x"))

show("等式拆双向：x + y == 5, y in [2,4]  =>  x in [1,3]")
s = Solver()
s.add_variable("x", 0, 10)
s.add_variable("y", 2, 4)
s.add_constraint([(1, "x"), (1, "y")], 5, sense="==")
print(f"x in [{s.lo['x']}, {s.hi['x']}], y in [{s.lo['y']}, {s.hi['y']}]")

show("共享变量队列传播至固定点：x<=y, y<=z, z<=3")
s = Solver()
for v in "xyz":
    s.add_variable(v, 0, 10)
s.add_constraint([(1, "x"), (-1, "y")], 0)
s.add_constraint([(1, "y"), (-1, "z")], 0)
s.add_constraint([(1, "z")], 3)
print({v: (s.lo[v], s.hi[v]) for v in "xyz"})

show("撤回约束后域恢复")
s = Solver()
s.add_variable("x", 0, 10)
s.add_constraint([(1, "x")], 8)
mark = s.checkpoint()
s.add_constraint([(1, "x")], 3)
print(f"加 x<=3 后: x in [{s.lo['x']}, {s.hi['x']}]")
s.undo(mark)
print(f"撤回后:   x in [{s.lo['x']}, {s.hi['x']}]")

show("模同余发现区间传播漏掉的无解：2x+2y+2z == 3")
s = Solver()
for v in "xyz":
    s.add_variable(v, 0, 5)
s.add_constraint([(2, "x"), (2, "y"), (2, "z")], 3, sense="==")
print("status:", s.solve(), "|", s.conflict)
s2 = Solver(use_congruence=False)
for v in "xyz":
    s2.add_variable(v, 0, 5)
s2.add_constraint([(2, "x"), (2, "y"), (2, "z")], 3, sense="==")
print("关闭同余（仅区间传播）:", s2.solve(), "<- 漏掉了，故只报 UNKNOWN")

show("大整数无溢出")
big = 2**127
s = Solver()
s.add_variable("x", -(2**210), 2**210)
s.add_constraint([(-big, "x")], 5 * big + 7)
print(f"-2^127 x <= 5*2^127+7  =>  x >= {s.lo['x']}")

show("零系数不影响域 / 常量矛盾直接失败")
s = Solver()
s.add_variable("x", -5, 5)
s.add_variable("y", -5, 5)
s.add_constraint([(0, "x"), (1, "y")], 3)
print(f"0*x+y<=3: x in [{s.lo['x']}, {s.hi['x']}], y in [{s.lo['y']}, {s.hi['y']}]")
s = Solver()
s.add_variable("x", 0, 5)
s.add_constraint([], -1)
print("0 <= -1:", s.solve(), "|", s.conflict)

show("声称有解时独立检查器验原约束")
s = Solver()
s.add_variable("x", 0, 10)
s.add_variable("y", 0, 10)
s.add_constraint([(2, "x")], 6, sense="==")
s.add_constraint([(1, "x"), (1, "y")], 5, sense="==")
status = s.solve()
assn = s.assignment()
print(f"status: {status}, assignment: {assn}, "
      f"independent check on original constraints: "
      f"{check_assignment(s.original, assn)}")
