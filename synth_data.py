"""本地合成的关系表（不调用任何 SQL 引擎）。

employees 分 3 个分区存放（模拟分布式数据源），包含：
- 重复工资（并列，用于验证稳定序）、NULL 工资（NULLS LAST）、
- NULL 部门（连接键 NULL 不匹配）、外键 e_dept -> departments.d_id。
departments 的 d_id 唯一，d_budget 含 NULL。
dept_yearly 的 d_id 不唯一（用于演示聚合下推被拒绝的情形）。
"""

EMPLOYEE_COLS = ["e_id", "e_name", "e_dept", "e_salary"]
EMPLOYEE_PARTS = [
    [(1, "Ada", 10, 90000), (2, "Ben", 20, 78000),
     (3, "Cid", 10, 72000), (4, "Dee", 30, None)],
    [(5, "Eli", 20, 85000), (6, "Fay", 10, 61000),
     (7, "Gil", None, 61000), (8, "Han", 30, 78000)],
    [(9, "Ivy", 10, 86000), (10, "Jay", 20, 94000),
     (11, "Kim", 30, 84000), (12, "Lou", 10, 88000)],
]

DEPARTMENT_COLS = ["d_id", "d_name", "d_budget"]
DEPARTMENT_PARTS = [[
    (10, "Eng", 500000),
    (20, "Ops", None),
    (30, "HR", 120000),
]]

DEPT_YEARLY_COLS = ["y_dept", "y_year", "y_budget"]
DEPT_YEARLY_PARTS = [[
    (10, 2023, 480000),
    (10, 2024, 500000),  # d_id 重复：连接键不唯一
    (20, 2024, 90000),
]]
