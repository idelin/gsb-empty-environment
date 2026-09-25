"""演示：合成数据全流程（可重放）。运行: python3.11 -m evidence_gc.demo"""

from __future__ import annotations

from . import catalog
from .interleave import enumerate_interleavings
from .model import content_hash, synthetic_content
from .reaper import CommitRejected, commit, create_plan, preview, replay_certificate
from .store import Store

NAMES = {
    "E1": "合同扫描件", "E2": "鉴定报告", "E3": "转账凭证", "E4": "聊天截图",
    "E5": "邮件导出", "E6": "附件包", "E7": "审计日志", "E8": "临时缓存",
}


def h(eid: str, ver: int) -> str:
    return content_hash(synthetic_content(eid, ver))


def fmt_chain(chain: list[dict]) -> str:
    parts = []
    for hop in chain:
        node = f"{hop['node'][0]}@v{hop['node'][1]}"
        if hop["kind"] == "root":
            parts.append(f"{node} [{hop['rule']}: {hop['detail']}]")
        elif hop["kind"] == "freeze_inherit":
            parts.append(f"-> {node} [{hop['rule']} 冻结继承 via {hop['ref_id']}]")
        else:
            parts.append(f"-> {node} [{hop['rule']} 引用钉住 via {hop['ref_id']}]")
    return " ".join(parts)


def build_store() -> Store:
    s = Store()
    # 事实：证据实例（合成内容哈希，可重放）
    for eid, created in (("E1", 0), ("E2", 0), ("E3", 0), ("E4", 0),
                         ("E5", 0), ("E6", 0), ("E7", 0), ("E8", 0)):
        s.add_evidence(eid, 1, created, h(eid, 1), tick=created)
    s.add_evidence("E2", 2, 2, h("E2", 2), tick=2)  # 鉴定报告修订版
    # 事实：自定义保留期策略（法定保留不在本系统范围）
    for pid, eid, ret in (("P1", "E1", 30), ("P2", "E2", 10), ("P3", "E3", 10),
                          ("P4", "E4", 5), ("P5", "E5", 10), ("P6", "E6", 5),
                          ("P7", "E7", 100), ("P8", "E8", 5)):
        s.grant_retention(pid, eid, ret, tick=2)
    # 事实：引用（钉住目标版本）
    s.add_reference("R1", ("E1", 1), ("E2", 2), tick=3)   # 活结论引用当前版本
    s.add_reference("R2", ("E2", 2), ("E3", 1), tick=4)
    s.add_reference("R3", ("E3", 1), ("E4", 1), tick=5)
    s.add_reference("R4", ("E7", 1), ("E2", 1), tick=6)   # 钉住旧版本 v1 → 无效
    s.add_reference("R5", ("E5", 1), ("E6", 1), tick=7)
    # 事实：显式冻结标记（可继承、可撤回）
    s.place_freeze("F1", ("E5", 1), tick=7)
    return s


def main() -> None:
    print("=" * 72)
    print("证据回收演示（合成数据 · Python 3.11 标准库 · 全部可重放）")
    print("范围说明：仅自定义保留期 + 显式冻结；法定保留不在本系统范围")
    print("=" * 72)

    store = build_store()
    print("\n[1] 事实登记（均带稳定 ID 与版本）")
    for eid in sorted(NAMES):
        vers = sorted(v for (e, v) in store.instances if e == eid)
        ret = max((p.retention_ticks for p in store.policies.values() if p.eid == eid), default=0)
        print(f"  {eid} {NAMES[eid]}: 版本{vers} 保留期{ret}t")
    print("  引用: R1 E1@v1->E2@v2, R2 E2@v2->E3@v1, R3 E3@v1->E4@v1,")
    print("        R4 E7@v1->E2@v1(钉住旧版本→无效), R5 E5@v1->E6@v1")
    print("  冻结: F1 -> E5@v1（沿 R5 继承到 E6@v1）")

    now = 25
    prev = preview(store, now)
    print(f"\n[2] 试算 preview@{now}（R-DRY-1：不实际删除，事件数={len(store.events)} 不变）")
    print(f"  可回收: {prev['reclaimable']}")
    print(f"  已过期: {prev['expired']}")
    print(f"  不可回收闭包: {prev['retained']}")

    print("\n[3] 引用阻塞链（最小不可回收闭包，R-CLO-1）")
    for node, chains in sorted(prev["blocking_chains"].items()):
        for chain in chains:
            print(f"  {fmt_chain(chain)}")

    plan_result = create_plan(store, now, tick=now)
    token = plan_result.plan["plan_token"]
    print(f"\n[4] 标记代际 gen{plan_result.plan['generation']} 计划（S1，token={token}）")
    print(f"  候选: {[c['node_id'] for c in plan_result.plan['candidates']]}")
    print(f"  版本快照: seq={plan_result.plan['snapshot']['seq']} "
          f"hash={plan_result.plan['snapshot']['event_prefix_hash'][:12]}...")

    store.add_reference("R6", ("E1", 1), ("E8", 1), tick=26)
    print("\n[5] 新引用到来 R6: E1@v1 -> E8@v1（t=26，必须阻止已计划删除，R-NEW-1）")

    cert1 = commit(store, token, 26, tick=26)
    print(f"\n[6] 提交 gen1（S2 版本验证 → S3 重算 → S4 删除）")
    print(f"  删除: {[d['node_id'] for d in cert1['deleted']]}")
    for b in cert1["blocked"]:
        print(f"  阻止: {b['node_id']} 类别={b['category']}")
        for chain in b["chains"]:
            print(f"    阻塞链: {fmt_chain(chain)}")

    live = cert1["liveness"]
    print(f"\n[7] 存活证明（R-PRF-1）: ok={live['ok']} 悬空依赖={live['dangling']} "
          f"无根存活者={live['survivors_without_root']}")

    replayed = replay_certificate(cert1)
    print(f"\n[8] 删除证书 {cert1['certificate_id']}（{cert1['kind']} v{cert1['schema_version']}）")
    print(f"  证书哈希: {cert1['certificate_hash'][:16]}...  "
          f"重放验证: verified={replayed['replay']['verified']}")

    store.revoke_freeze("F1", tick=27)
    prev2 = preview(store, 27)
    print(f"\n[9] 解冻 F1（t=27，R-FRZ-2 重新计算）: 可回收变为 {prev2['reclaimable']}")

    plan2 = create_plan(store, 27, tick=28)
    cert2 = commit(store, plan2.plan["plan_token"], 28, tick=28)
    replayed2 = replay_certificate(cert2)
    print(f"\n[10] 提交 gen2: 删除 {[d['node_id'] for d in cert2['deleted']]} "
          f"证书 {cert2['certificate_id']} 重放 verified={replayed2['replay']['verified']}")

    print("\n[11] 应用前版本验证（R-CMT-1）")
    try:
        commit(store, token, 29, tick=29)
    except CommitRejected as exc:
        print(f"  重复提交 gen1 被拒绝: {exc}")
    plan3 = create_plan(store, 29, tick=29)
    plan4 = create_plan(store, 29, tick=29)
    try:
        commit(store, plan3.plan["plan_token"], 29, tick=29)
    except CommitRejected as exc:
        print(f"  过期代际计划被拒绝: {exc}")
    print(f"  （gen{plan4.plan['generation']} 为当前代际）")

    print("\n[12] 交错枚举：引用新增 × 解冻 × 回收提交（3! = 6 种）")
    for r in enumerate_interleavings():
        print(f"  顺序 {' → '.join(r['order'])}")
        for line in r["trace"]:
            print(f"    - {line}")
        if r["followup_gen2"]:
            print(f"    - 收尾 gen2: 删除{r['followup_gen2']['deleted']}")
        print(f"    最终存活: {r['final_survivors']}")

    print("\n[13] 规则与步骤目录（稳定 ID / 版本）")
    for rid, meta in catalog.RULES.items():
        print(f"  {rid} v{meta['version']}: {meta['desc']}")
    for sid, meta in catalog.STEPS.items():
        print(f"  {sid} v{meta['version']}: {meta['desc']}")


if __name__ == "__main__":
    main()
