"""本地测试夹具：部门（左表）与员工（右表）按 dept_id 左外连接。

所有变更都是本地构造的 Change 列表：带事务ID、表、主键、前值/后值、
提交序号。* 没有消息平台 *，demo 与测试直接消费这些列表。
"""
from __future__ import annotations

from view import Change

LEFT_TABLE = "departments"
RIGHT_TABLE = "employees"
JOIN_KEY = "dept_id"


def dept(pk: int, dept_id: str, name: str) -> dict:
    return {"pk": pk, "dept_id": dept_id, "dname": name}


def emp(pk: int, dept_id: str, name: str) -> dict:
    return {"pk": pk, "dept_id": dept_id, "ename": name}


def changelog() -> list[Change]:
    """一条按提交序号排列的变更时间线。"""
    return [
        # seq=1: 先到一个左行，右侧无匹配 -> NULL 扩展行
        Change("T1", LEFT_TABLE, 1, None, dept(1, "ENG", "Engineering"), 1),
        # seq=2: 右表首个匹配到来 -> 撤销 NULL，发布匹配行
        Change("T2", RIGHT_TABLE, 101, None, emp(101, "ENG", "Alice"), 2),
        # seq=3: 同键第二个右匹配 -> 输出共 2 行（左一右二，重数 2）
        Change("T3", RIGHT_TABLE, 102, None, emp(102, "ENG", "Bob"), 3),
        # seq=4: 同一个事务里：删一条右匹配 + 插入一个新左部门 +
        #         给新部门插入唯一员工。最终一致结果：
        #         ENG 仍有 1 个匹配（不补 NULL）；HR 是正常匹配行（无 NULL 摆动）
        Change("T4", RIGHT_TABLE, 101, emp(101, "ENG", "Alice"), None, 4),
        Change("T4", LEFT_TABLE, 2, None, dept(2, "HR", "Human Resources"), 4),
        Change("T4", RIGHT_TABLE, 201, None, emp(201, "HR", "Carol"), 4),
        # seq=5: 删除 ENG 最后一个右匹配 -> 恢复 NULL 扩展行
        Change("T5", RIGHT_TABLE, 102, emp(102, "ENG", "Bob"), None, 5),
        # seq=6: 同事务内左右表联动——ENG 更新部门名，同时插入其首个员工：
        #         最终结果只发布更新后的匹配行（NULL 撤销在事务内抵消，不外泄）
        Change("T6", LEFT_TABLE, 1, dept(1, "ENG", "Engineering"), dept(1, "ENG", "Engineering Team"), 6),
        Change("T6", RIGHT_TABLE, 103, None, emp(103, "ENG", "Dan"), 6),
        # seq=7: 员工跨部门调动（ENG -> HR），ENG 恢复 NULL，HR 多重匹配
        Change("T7", RIGHT_TABLE, 201,
               emp(201, "HR", "Carol"), emp(201, "SALES", "Carol"), 7),
        Change("T7", LEFT_TABLE, 3, None, dept(3, "SALES", "Sales"), 7),
        # seq=8: 同事务内删后插同一个右主键（净无变化）-> 不发布任何行
        Change("T8", RIGHT_TABLE, 103, emp(103, "ENG", "Dan"), None, 8),
        Change("T8", RIGHT_TABLE, 103, None, emp(103, "ENG", "Dan"), 8),
        # seq=9: SALES 先增加 Frank，再删除 Carol；删除后仍剩 1 个匹配
        #         -> 只删 1 条匹配行，不补 NULL 扩展行
        Change("T9", RIGHT_TABLE, 301, None, emp(301, "SALES", "Frank"), 9),
        Change("T9", RIGHT_TABLE, 201, emp(201, "SALES", "Carol"), None, 9),
    ]
