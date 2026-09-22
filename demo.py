"""运行样例：emp ⋈ dept 后按部门分组统计人数与工资和。

流水线：emp -> Filter(age>=30) -> Project(name,dept_id,salary)
        dept --------------------> Join(dept_id) -> Project(dname,salary)
                                  -> GroupBy(dname) -> View
"""
from ivm import (
    DuplicateRowError, Filter, GroupBy, Join, NegativeMultiplicityError,
    Project, Relation, UnknownRowError, View, ge,
)


def build():
    dept = Relation("dept")          # (dept_id, dname)
    emp = Relation("emp")            # (name, dept_id, salary, age)
    adults = Filter(ge(3, 30))       # age 为 None 或 <30 的行被过滤
    emp_proj = Project((0, 1, 2))    # (name, dept_id, salary)
    join = Join(1, 0)                # emp.dept_id = dept.dept_id
    by_dept = Project((4, 2))        # 连接结果 (name,dept_id,salary,dept_id,dname) -> (dname, salary)
    grp = GroupBy((0,), 1)           # (dname, count, sum_salary)
    view = View("dept_stats")
    emp.outputs.append(adults.feed)
    adults.outputs.append(emp_proj.feed)
    emp_proj.outputs.append(join.feed_left)
    dept.outputs.append(join.feed_right)
    join.outputs.append(by_dept.feed)
    by_dept.outputs.append(grp.feed)
    grp.outputs.append(view.feed)
    return dept, emp, view


def show(title, view, since):
    print(f"== {title} ==")
    for row, m in view.log[since:]:
        print(f"  差量 {m:+d}  {row}")
    print("  视图:", {r: c for r, c in view.rows()} or "空")
    print()


def main():
    dept, emp, view = build()
    n = 0

    dept.load([("d1", (1, "Eng")), ("d2", (2, "Ops"))])
    emp.load([
        ("e1", ("Ann", 1, 100, 35)),
        ("e2", ("Bob", 1, 150, 41)),
        ("e3", ("Cy", 2, 120, 28)),    # 年龄 <30，被过滤
        ("e4", ("Dan", 2, None, 50)),  # 工资 NULL：计数计入、求和忽略
        ("e5", ("Eve", None, 90, 60)), # 部门 NULL：不参与连接
    ])
    show("初始装载", view, n)
    n = len(view.log)

    # 重复行：投影后重数为 2，连接输出按对方重数成倍
    emp.load([("e6", ("Eve", 1, 100, 33)), ("e7", ("Eve", 1, 100, 33))])
    show("插入重复行 e6/e7（连接一侧重数相乘）", view, n)
    n = len(view.log)

    # 撤回最后支撑行：Ops 组仅剩 e4，删除后整组输出被撤回
    emp.delete("e4")
    show("删除 e4（Ops 组最后支撑行被撤回）", view, n)
    n = len(view.log)

    emp.delete("e6")
    emp.delete("e7")
    show("删除 e6/e7（重复行逐条撤回）", view, n)
    n = len(view.log)

    # 批次交换顺序最终结果一致
    batch_a = [("insert", "e8", ("Fin", 1, 130, 45))]
    batch_b = [("delete", "e1", None), ("insert", "e9", ("Gem", 2, 80, 52))]

    def apply(emp_rel, batches):
        for batch in batches:
            for op, rid, row in batch:
                getattr(emp_rel, op)(rid, row) if row else getattr(emp_rel, op)(rid)

    dept1, emp1, view1 = build()
    dept1.load([("d1", (1, "Eng")), ("d2", (2, "Ops"))])
    emp1.load([("e1", ("Ann", 1, 100, 35)), ("e2", ("Bob", 1, 150, 41))])
    apply(emp1, [batch_a, batch_b])

    dept2, emp2, view2 = build()
    dept2.load([("d1", (1, "Eng")), ("d2", (2, "Ops"))])
    emp2.load([("e1", ("Ann", 1, 100, 35)), ("e2", ("Bob", 1, 150, 41))])
    apply(emp2, [batch_b, batch_a])

    print("== 批次交换顺序 ==")
    print("  A->B:", {r: c for r, c in view1.rows()})
    print("  B->A:", {r: c for r, c in view2.rows()})
    print("  最终视图一致:", view1.state == view2.state)
    print()

    # 错误语义
    print("== 错误处理 ==")
    try:
        emp.delete("e4")
    except UnknownRowError as exc:
        print("  重复删除同一行:", exc)
    try:
        emp.insert("e1", ("Ann", 1, 100, 35))
    except DuplicateRowError as exc:
        print("  重复插入行 ID:", exc)
    try:
        view.feed([(("Ghost", 1, 1), -1)])
    except NegativeMultiplicityError as exc:
        print("  负重数:", exc)


if __name__ == "__main__":
    main()
