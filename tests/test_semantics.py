"""NULL 与多重集合（bag）语义测试。"""

import unittest

from subplan.plan import Filter, Join, Project, Scan
from subplan.relation import Table, sql_eq, sql_gt


def emp_table():
    # 含重复行 (2,'b',10,None)，含 NULL 的 dept 与 salary
    return Table('emp', ('id', 'name', 'dept', 'salary'), [
        (1, 'a', 10, 100),
        (2, 'b', 10, None),
        (3, 'c', None, 300),
        (2, 'b', 10, None),   # 完全重复的一行
    ])


def dept_table():
    # 含重复行 (10, 'x')
    return Table('dept', ('did', 'dname'), [
        (10, 'x'),
        (10, 'x'),
        (20, 'y'),
    ])


class TestNullSemantics(unittest.TestCase):
    def test_sql_compare_with_null_is_unknown(self):
        self.assertIsNone(sql_eq(None, None))
        self.assertIsNone(sql_eq(None, 1))
        self.assertIsNone(sql_gt(None, 1))
        self.assertIs(sql_eq(1, 1), True)
        self.assertIs(sql_gt(2, 1), True)

    def test_filter_drops_unknown(self):
        scan = Scan(emp_table())
        node = Filter(scan, lambda r: sql_gt(r[3], 50), label='sal>50')
        rows = list(node.iter_rows())
        # salary 为 None 的两行（含重复行）被丢弃，只剩 100 与 300
        self.assertEqual(sorted(r[0] for r in rows), [1, 3])

    def test_join_null_key_never_matches(self):
        join = Join(Scan(emp_table()), Scan(dept_table()), 2, 0)
        rows = list(join.iter_rows())
        # dept 为 NULL 的行 (id=3) 不产生任何连接结果
        self.assertNotIn(3, [r[0] for r in rows])


class TestBagSemantics(unittest.TestCase):
    def test_filter_preserves_duplicates(self):
        scan = Scan(emp_table())
        node = Filter(scan, lambda r: sql_eq(r[1], 'b'), label="name='b'")
        rows = list(node.iter_rows())
        self.assertEqual(len(rows), 2)  # 重复行保留

    def test_project_preserves_duplicates(self):
        scan = Scan(emp_table())
        node = Project(scan, (2,))
        rows = list(node.iter_rows())
        self.assertEqual(rows.count((10,)), 3)
        self.assertEqual(rows.count((None,)), 1)

    def test_join_multiplies_duplicates(self):
        join = Join(Scan(emp_table()), Scan(dept_table()), 2, 0)
        rows = list(join.iter_rows())
        # 左表 dept=10 有 3 行，右表 did=10 有 2 行 → 3×2=6
        self.assertEqual(len(rows), 6)
        # 左表重复行 ×2 与右表重复行 ×2 乘法展开 → 4 条相同结果
        self.assertEqual(rows.count((2, 'b', 10, None, 10, 'x')), 4)


if __name__ == '__main__':
    unittest.main()
