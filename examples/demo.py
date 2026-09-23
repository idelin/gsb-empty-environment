"""运行样例：公共子计划的物化 / 重算 / 溢写决策与共享执行。

运行：python3 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from subplan import Engine, Filter, Join, Scan, Table, largest_common_subplan


def build():
    emp = Table('emp', ('id', 'name', 'dept', 'salary'), [
        (1, 'alice', 10, 90000),
        (2, 'bob', 10, 55000),
        (3, 'carol', 20, 72000),
        (4, 'dave', None, 61000),   # NULL 部门：不参与等值连接
        (5, 'erin', 20, None),      # NULL 薪水：三值逻辑下被 sal 过滤丢弃
        (3, 'carol', 20, 72000),    # 重复行：bag 语义下保留
    ])
    dept = Table('dept', ('did', 'dname'), [
        (10, 'Engineering'),
        (20, 'Sales'),
    ])
    shared = Join(Scan(emp), Scan(dept), 2, 0, estimate=4)
    q_high_salary = Filter(shared, lambda r: r[3] is not None and r[3] > 60000,
                           label='sal>60000')
    q_engineering = Filter(shared, lambda r: r[5] == 'Engineering',
                           label="dept='Engineering'")
    return shared, q_high_salary, q_engineering


def show(title):
    print('=' * 64)
    print(title)
    print('=' * 64)


def main():
    shared, q_high_salary, q_engineering = build()
    common = largest_common_subplan(q_high_salary, q_engineering)
    print('两个查询的公共子计划（过滤之前的部分）：', common.signature()[0])

    show('场景 1：内存充足，2 个消费者 → 内存物化')
    engine = Engine(memory_budget=1 << 20)
    decision, iterators = engine.run_shared(shared, consumers=2)
    print(decision.rationale())
    high = [r for r in iterators[0] if q_high_salary.predicate(r) is True]
    eng = [r for r in iterators[1] if q_engineering.predicate(r) is True]
    print('Q1（salary>60000）:', sorted(r[1] for r in high))
    print("Q2（dept='Engineering'）:", sorted(r[1] for r in eng))
    print('物化的共享结果（未过滤，含重复行 carol×2）:',
          sorted(r[1] for r in engine.store.get(shared.signature())))
    engine.store.close()

    show('场景 2：内存预算极小、8 个消费者 → 溢写物化')
    engine = Engine(memory_budget=64)
    decision, iterators = engine.run_shared(shared, consumers=8)
    print(decision.rationale())
    print('溢写后读回 Q1 结果:', sorted(
        r[1] for r in iterators[0] if q_high_salary.predicate(r) is True))
    engine.store.close()

    show('场景 3：仅 1 个消费者 → 不无故物化，直接重算')
    engine = Engine(memory_budget=1 << 20)
    decision, iterators = engine.run_shared(shared, consumers=1)
    print(decision.rationale())
    print('物化存储为空:', shared.signature() not in engine.store)
    engine.store.close()


if __name__ == '__main__':
    main()
