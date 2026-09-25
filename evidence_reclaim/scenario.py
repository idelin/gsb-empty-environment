"""合成样例数据（非真实数据，可完整重放）。

时间轴（模拟时钟，秒）：
  t=0    创建 7 个证据节点版本
  t=10   建立引用 r1(ev1->ev2)、r2(ev3->ev4)、r3(ev5->ev6)
  t=20   撤销 r3（撤销的引用不再提供保护）
  t=30   对 ev3 施加显式冻结 f1
  t=500  试算/提交（now=500）

节点（retention 为法定无关的自定义保留期，None 表示永久保留）：
  ev1#v1  retention=1000  未过期            -> 保留期根
  ev2#v1  retention=100   已过期, 被 r1 引用 -> 过期但被活结论引用，不可回收
  ev3#v1  retention=100   已过期, 冻结 f1    -> 冻结根
  ev4#v1  retention=100   已过期, 被 ev3 引用 -> 冻结继承
  ev5#v1  retention=100   已过期, 无有效引用  -> 可回收
  ev6#v1  retention=100   已过期, 仅被已撤销的 r3 引用 -> 可回收
  ev7#v1  retention=None  永久保留           -> 保留期根
"""

from __future__ import annotations

from .engine import ReclaimEngine

NOW = 500

NODE_SPECS = [
    ("ev1", 1, 0, 1000, "alpha-report"),
    ("ev2", 1, 0, 100, "beta-attachment"),
    ("ev3", 1, 0, 100, "gamma-log"),
    ("ev4", 1, 0, 100, "delta-trace"),
    ("ev5", 1, 0, 100, "epsilon-temp"),
    ("ev6", 1, 0, 100, "zeta-temp"),
    ("ev7", 1, 0, None, "eta-charter"),
]


def build_sample_engine() -> ReclaimEngine:
    engine = ReclaimEngine()
    for evidence_id, version, created_at, retention, payload in NODE_SPECS:
        engine.add_node(evidence_id, version, created_at, retention, payload)
    engine.add_reference("r1", ("ev1", 1), ("ev2", 1), created_at=10)
    engine.add_reference("r2", ("ev3", 1), ("ev4", 1), created_at=10)
    engine.add_reference("r3", ("ev5", 1), ("ev6", 1), created_at=10)
    engine.revoke_reference("r3")
    engine.add_freeze("f1", ("ev3", 1), created_at=30)
    return engine


NEW_REF_SPEC = {"ref_id": "r-new", "source": ("ev1", 1), "target": ("ev5", 1), "created_at": 600}
FREEZE_ID = "f1"
