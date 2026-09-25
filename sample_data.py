"""合成样例数据：员工系统权限场景，t1 -> t2 两个观察时刻。

t1 -> t2 的变化覆盖全部类别：
- 新增事实：f7~f10（bob 的入职信息）-> bob 获得 repo_access
- 规则变更：r2 v1 -> v2（同 ID 内容变更，新增 device 条件）
- 证据失效：f5 被删除（alice 的安全培训记录）-> alice 失去 prod_deploy
- 无关输入：f6 版本更新、f11 新增（无任何规则引用，不进入解释）
"""
from engine import Fact, Rule
from causaldiff import Snapshot

R1 = Rule(
    "r1", 1,
    (("employee", ("?x",)), ("department", ("?x", "eng"))),
    ("eligible_eng_tools", ("?x",)),
)
R2_V1 = Rule(
    "r2", 1,
    (("eligible_eng_tools", ("?x",)), ("clearance", ("?x", "l2"))),
    ("grant", ("?x", "repo_access")),
)
R2_V2 = Rule(
    "r2", 2,
    (("eligible_eng_tools", ("?x",)),
     ("clearance", ("?x", "l2")),
     ("device", ("?x", "?d"))),
    ("grant", ("?x", "repo_access")),
)
R3 = Rule(
    "r3", 1,
    (("grant", ("?x", "repo_access")), ("training", ("?x", "security101"))),
    ("grant", ("?x", "prod_deploy")),
)


def _f(fid, version, pred, *args):
    return Fact(fid, version, pred, tuple(args))


def snapshot_t1() -> Snapshot:
    facts = (
        _f("f1", 1, "employee", "alice"),
        _f("f2", 1, "department", "alice", "eng"),
        _f("f3", 1, "clearance", "alice", "l2"),
        _f("f4", 1, "device", "alice", "laptop1"),
        _f("f5", 1, "training", "alice", "security101"),
        _f("f6", 1, "project", "bob", "alpha"),
    )
    return Snapshot("t1", facts, (R1, R2_V1, R3))


def snapshot_t2() -> Snapshot:
    facts = (
        _f("f1", 1, "employee", "alice"),
        _f("f2", 1, "department", "alice", "eng"),
        _f("f3", 1, "clearance", "alice", "l2"),
        _f("f4", 1, "device", "alice", "laptop1"),
        # f5 删除：证据失效
        _f("f6", 2, "project", "bob", "beta"),        # 无关变更
        _f("f7", 1, "employee", "bob"),               # 以下为新增事实
        _f("f8", 1, "department", "bob", "eng"),
        _f("f9", 1, "clearance", "bob", "l2"),
        _f("f10", 1, "device", "bob", "laptop2"),
        _f("f11", 1, "coffee_pref", "alice", "latte"),  # 无关新增
    )
    return Snapshot("t2", facts, (R1, R2_V2, R3))  # r2 同 ID 内容变更 v1->v2
