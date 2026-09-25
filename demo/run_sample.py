"""运行样例（合成数据，可重放）。

用法: python3.11 demo/run_sample.py
"""

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from evidence_reclaim.engine import ReclaimEngine, replay_certificate
from evidence_reclaim.interleave import enumerate_interleavings
from evidence_reclaim.rules import RULES
from evidence_reclaim.scenario import FREEZE_ID, NEW_REF_SPEC, NOW, build_sample_engine


def show(title):
    print("\n" + "=" * 72)
    print(title)
    print("=" * 72)


def main():
    show("1. 规则注册表（稳定 ID + 版本）")
    for rule in RULES.values():
        print(f"  {rule.rule_id} v{rule.version}  {rule.title}")

    engine = build_sample_engine()
    show(f"2. 合成数据已载入：{len(engine.log.store.nodes)} 节点版本，"
         f"{len(engine.log.store.refs)} 引用，{len(engine.log.store.freezes)} 冻结；"
         f"存储版本 v{engine.log.version}")

    show(f"3. 试算（dry-run，now={NOW}）：最小不可回收闭包 vs 可回收集合")
    plan = engine.plan(NOW)
    print(f"  计划 {plan.plan_id}  代际={plan.generation}  基线版本=v{plan.base_version}")
    print(f"  受保护: {sorted(f'{k[0]}#v{k[1]}' for k in plan.protected)}")
    print(f"  可回收: {sorted(f'{k[0]}#v{k[1]}' for k in plan.candidates)}")

    show("4. 引用阻塞链（过期但不可回收的节点）")
    for entry in plan.blocked:
        node = entry["node"]
        print(f"  {node[0]}#v{node[1]} (expires_at={entry['expires_at']}):")
        for step in entry["chain"]:
            via = f" via {step['via_ref']}" if step["via_ref"] else ""
            node = f"{step['node'][0]}#v{step['node'][1]}"
            print(f"    [{step['step_id'][:20]}...] {node}  {step['reason']}{via}"
                  f"  规则 {step['rule_id']} v{step['rule_version']}")
            print(f"        {step['detail']}")

    show("5. 新引用到来 -> 已计划删除被阻止（提交验证）")
    engine.add_reference(NEW_REF_SPEC["ref_id"], NEW_REF_SPEC["source"],
                         NEW_REF_SPEC["target"], NEW_REF_SPEC["created_at"])
    cert, rejection = engine.commit(plan)
    print(f"  提交结果: 拒绝 ({rejection.rejection_id})")
    for reason in rejection.reasons:
        print(f"    - {reason}")
    for node, chain in rejection.blocked_chains.items():
        print(f"    阻塞链 {node}: " + " -> ".join(f"{s['node'][0]}#v{s['node'][1]}" for s in chain))
    print(f"  提示以代际 {rejection.new_generation_hint} 重新试算")

    show("6. 解冻后重新计算 + 重新试算 + 提交")
    engine.revoke_freeze(FREEZE_ID)
    plan2 = engine.plan(NOW)
    print(f"  新计划 {plan2.plan_id}  代际={plan2.generation}")
    print(f"  可回收: {sorted(f'{k[0]}#v{k[1]}' for k in plan2.candidates)}")
    cert, rejection = engine.commit(plan2)
    assert cert is not None, rejection
    print(f"  提交成功，删除证书 {cert.cert_id}")
    print(f"  删除节点: {sorted(f'{k[0]}#v{k[1]}' for k in cert.deleted_nodes)}")
    print(f"  删除引用: {cert.deleted_refs}")
    print(f"  存活证明: ok={cert.liveness.ok} 悬空引用={cert.liveness.dangling_refs}"
          f"  post_version=v{cert.liveness.post_version}")

    show("7. 证书重放验证 + 日志重放")
    problems = replay_certificate(engine, cert)
    print(f"  证书重放问题: {problems if problems else '无（通过）'}")
    rebuilt = engine.replay()
    print(f"  日志重放: 状态根一致，版本 v{rebuilt.log.version}")
    print("  证书内容:")
    print("  " + json.dumps(cert.to_dict(), ensure_ascii=False, indent=2).replace("\n", "\n  "))

    show("8. 交错枚举：新引用 / 解冻 / 提交 的全部 6 种顺序")
    base = build_sample_engine()
    base_plan = base.plan(NOW)
    scenarios = enumerate_interleavings(base, base_plan, NOW, NEW_REF_SPEC, FREEZE_ID)
    for s in scenarios:
        print(f"  顺序 {' -> '.join(s.order)}")
        for o in s.outcomes:
            mark = "OK " if o.admitted else "NO "
            print(f"    [{mark}] {o.op}: {o.detail}")
        print(f"    提交={s.commit_status}  存活证明={'通过' if s.liveness_ok else '失败'}"
              f"  剩余={s.remaining_nodes}")
        print(f"    之后可回收={s.recyclable_after}")

    show("9. 试算不实际删除：dry-run 前后状态一致")
    probe = build_sample_engine()
    before = probe.log.store.state_root()
    probe.plan(NOW)
    probe.plan(NOW + 100)
    print(f"  两次试算后状态根不变: {before == probe.log.store.state_root()}")
    print(f"  日志事件数不变: {len(probe.log.events)}")


if __name__ == "__main__":
    main()
