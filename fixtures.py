"""本地夹具：事务变更事件流，不接消息平台。

事件为普通 Python 对象列表，可按任意顺序喂给 Applier，模拟：
缓冲、提交标记早到、事务内先删后插同键、提交序号缺口、回滚丢弃、
重复事务重放，以及“重启后续传”。
"""

from __future__ import annotations

from typing import List, Union

from txbuffer import Abort, Change, Commit, Applier

Event = Union[Change, Commit, Abort]


# --- 第一批：进程崩溃前的乱序事件流 -----------------------------------------
#
# T1 commit_seq=1  建账（两条变更，跨 accounts/ledger 两表）
# T2 commit_seq=2  账户 1 上先删后插同键（按事务内序号序执行）
# T3 commit_seq=3  提交标记早到：Commit 在全部 Change 之前到达
# T4 commit_seq=4  只有部分变更到达 + Commit（半事务），重启后必须不可见
# T5                 回滚丢弃
# T6 commit_seq=6  完整就绪，但因缺少 commit_seq=5 而暂停
#
# T1
T1_C1 = Change("T1", 1, "accounts", 1, None, {"name": "alice", "bal": 100})
T1_C2 = Change("T1", 2, "ledger", "L1", None, {"acct": 1, "delta": 100})
T1_COMMIT = Commit("T1", 1, 2)

# T2：先把账户 1 删除，再以同键插入新值；最终账户 1 为新值
T2_C1 = Change("T2", 1, "accounts", 1, {"name": "alice", "bal": 100}, None)
T2_C2 = Change("T2", 2, "accounts", 1, None, {"name": "alice2", "bal": 150})
T2_COMMIT = Commit("T2", 2, 2)

# T3：提交标记早到（change_count=2 但尚无变更）
T3_COMMIT = Commit("T3", 3, 2)
T3_C1 = Change("T3", 1, "accounts", 2, None, {"name": "bob", "bal": 200})
T3_C2 = Change("T3", 2, "accounts", 2, {"name": "bob", "bal": 200}, {"name": "bob", "bal": 250})

# T4：只有第 1 条变更和提交标记到达，第 2 条“在崩溃中丢失”
T4_C1 = Change("T4", 1, "accounts", 3, None, {"name": "carol", "bal": 300})
T4_COMMIT = Commit("T4", 4, 2)

# T5：回滚
T5_C1 = Change("T5", 1, "accounts", 9, None, {"name": "ghost", "bal": 1})
T5_ABORT = Abort("T5")

# T6：完整，但 commit_seq=5 缺失 -> 暂停
T6_C1 = Change("T6", 1, "accounts", 2, {"name": "bob", "bal": 250}, {"name": "bob", "bal": 260})
T6_COMMIT = Commit("T6", 6, 1)

FIRST_BATCH: List[Event] = [
    T6_COMMIT,                      # 6 号先到，无处可提
    T6_C1,                          # T6 完整 -> ready，但缺口 5，暂停
    T3_COMMIT,                      # 提交标记早到 -> waiting
    T1_C1,
    T2_C1,
    T1_C2,
    T5_C1,
    T1_COMMIT,                      # T1 完整 -> 立即原子应用（seq=1）
    T2_C2,
    T2_COMMIT,                      # T2 完整 -> 立即应用（seq=2）
    T3_C1,
    T3_C2,                          # 清单补齐 -> T3 自动应用（seq=3）
    T4_C1,
    T4_COMMIT,                      # 半事务：缺第 2 条 -> 一直 waiting
    T5_ABORT,                       # T5 丢弃
    T1_C1,                          # 重复变更：不二次生效
    T1_COMMIT,                      # 重复提交：不二次生效
]

# --- 第二批：“重启”后补传 ----------------------------------------------------
#
# T4 重新完整重传（事务 ID 相同）；提交序号 5 出现后，6 自动续上。
# 另外混入 T2 的重复提交，验证不二次生效。
SECOND_BATCH: List[Event] = [
    Change("T4", 1, "accounts", 3, None, {"name": "carol", "bal": 300}),
    Commit("T4", 4, 2),             # 仍是半事务（旧实例的缓冲已随重启消失）
    Change("T4", 1, "accounts", 3, None, {"name": "carol", "bal": 300}),
    Change("T4", 2, "accounts", 3, {"name": "carol", "bal": 300}, {"name": "carol", "bal": 320}),
    Commit("T4", 4, 2),             # 清单补齐 -> seq=4
    Change("T7", 1, "ledger", "L2", None, {"acct": 2, "delta": 10}),
    Commit("T7", 5, 1),             # 缺口 5 补上
    # T6 在旧实例中曾就绪并暂停；其内存队列随重启消失，由重传补回
    Change("T6", 1, "accounts", 2, {"name": "bob", "bal": 250}, {"name": "bob", "bal": 260}),
    Commit("T6", 6, 1),             # 4/5/6 至此连续应用
    T2_COMMIT,                      # 重复事务：duplicate，不二次生效
]


def replay(applier: Applier, events: List[Event]) -> List[str]:
    results = []
    for event in events:
        if isinstance(event, Change):
            results.append(applier.handle_change(event))
        elif isinstance(event, Commit):
            results.append(applier.handle_commit(event))
        else:
            results.append(applier.handle_abort(event))
    return results
