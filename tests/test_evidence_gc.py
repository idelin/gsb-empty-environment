"""证据回收系统测试（标准库 unittest；全部使用合成数据）。"""

import unittest

from evidence_gc import catalog
from evidence_gc.interleave import enumerate_interleavings
from evidence_gc.model import content_hash, synthetic_content
from evidence_gc.reaper import CommitRejected, commit, create_plan, preview, replay_certificate
from evidence_gc.store import Store, StaleTickError


def h(eid, ver):
    return content_hash(synthetic_content(eid, ver))


def build_store():
    """与演示一致的合成场景。"""
    s = Store()
    for eid in ("E1", "E2", "E3", "E4", "E5", "E6", "E7", "E8"):
        s.add_evidence(eid, 1, 0, h(eid, 1), tick=0)
    s.add_evidence("E2", 2, 2, h("E2", 2), tick=2)
    for pid, eid, ret in (("P1", "E1", 30), ("P2", "E2", 10), ("P3", "E3", 10),
                          ("P4", "E4", 5), ("P5", "E5", 10), ("P6", "E6", 5),
                          ("P7", "E7", 100), ("P8", "E8", 5)):
        s.grant_retention(pid, eid, ret, tick=2)
    s.add_reference("R1", ("E1", 1), ("E2", 2), tick=3)
    s.add_reference("R2", ("E2", 2), ("E3", 1), tick=4)
    s.add_reference("R3", ("E3", 1), ("E4", 1), tick=5)
    s.add_reference("R4", ("E7", 1), ("E2", 1), tick=6)
    s.add_reference("R5", ("E5", 1), ("E6", 1), tick=7)
    s.place_freeze("F1", ("E5", 1), tick=7)
    return s


class ClosureTests(unittest.TestCase):
    def test_closure_exact_sets(self):
        store = build_store()
        from evidence_gc.closure import compute_closure
        c = compute_closure(store, 25)
        self.assertEqual(c.reclaimable, {("E2", 1), ("E8", 1)})
        self.assertEqual(c.retained, {
            ("E1", 1), ("E2", 2), ("E3", 1), ("E4", 1),
            ("E5", 1), ("E6", 1), ("E7", 1),
        })

    def test_expired_but_referenced_not_reclaimable(self):
        store = build_store()
        from evidence_gc.closure import compute_closure
        c = compute_closure(store, 25)
        for k in (("E2", 2), ("E3", 1), ("E4", 1)):
            self.assertIn(k, c.expired)
            self.assertIn(k, c.retained, "过期但仍被活结论引用不可回收")
        chain = max(c.chains[("E4", 1)], key=len)
        self.assertEqual([hop["rule"] for hop in chain],
                         ["R-RET-1", "R-REF-1", "R-REF-1", "R-REF-1"])
        self.assertEqual([hop.get("ref_id") for hop in chain[1:]], ["R1", "R2", "R3"])

    def test_stale_version_reference_is_invalid(self):
        store = build_store()
        from evidence_gc.closure import compute_closure, compute_valid_edges
        edge_ids = {rid for rid, _, _ in compute_valid_edges(store)}
        self.assertEqual(edge_ids, {"R1", "R2", "R3", "R5"})
        self.assertNotIn("R4", edge_ids, "钉住旧版本 v1 的引用无效")
        c = compute_closure(store, 25)
        self.assertIn(("E2", 1), c.reclaimable)
        # 引用失效后，仅靠该引用链保留的实例重新变为可回收
        store.revoke_reference("R1", tick=25)
        c2 = compute_closure(store, 25)
        for k in (("E2", 2), ("E3", 1), ("E4", 1)):
            self.assertIn(k, c2.reclaimable)

    def test_freeze_inheritance_and_revoke(self):
        store = build_store()
        from evidence_gc.closure import compute_closure
        c = compute_closure(store, 25)
        self.assertIn(("E6", 1), c.retained, "冻结沿有效引用继承")
        chain = max(c.chains[("E6", 1)], key=len)
        self.assertEqual([hop["rule"] for hop in chain], ["R-FRZ-1", "R-FRZ-1"])
        self.assertEqual([hop["kind"] for hop in chain], ["root", "freeze_inherit"])
        store.revoke_freeze("F1", tick=26)
        c2 = compute_closure(store, 26)
        self.assertIn(("E5", 1), c2.reclaimable)
        self.assertIn(("E6", 1), c2.reclaimable)


class ReaperTests(unittest.TestCase):
    def test_dry_run_does_not_mutate(self):
        store = build_store()
        n_events, n_instances = len(store.events), len(store.instances)
        result = preview(store, 25)
        self.assertTrue(result["dry_run"])
        self.assertEqual(len(store.events), n_events)
        self.assertEqual(len(store.instances), n_instances)

    def test_new_reference_blocks_planned_deletion(self):
        store = build_store()
        token = create_plan(store, 25, tick=25).plan["plan_token"]
        store.add_reference("R6", ("E1", 1), ("E8", 1), tick=26)
        cert = commit(store, token, 26, tick=26)
        self.assertEqual([d["node_id"] for d in cert["deleted"]], ["E2@v1"])
        blocked = {b["node_id"]: b for b in cert["blocked"]}
        self.assertEqual(set(blocked), {"E8@v1"})
        self.assertEqual(blocked["E8@v1"]["category"], "referenced")
        rules = [hop["rule"] for hop in blocked["E8@v1"]["chains"][0]]
        self.assertEqual(rules, ["R-RET-1", "R-REF-1"])
        self.assertIn(("E8", 1), store.instances, "被阻止的候选不得删除")

    def test_liveness_proof_has_no_dangling(self):
        store = build_store()
        token = create_plan(store, 25, tick=25).plan["plan_token"]
        store.add_reference("R6", ("E1", 1), ("E8", 1), tick=26)
        cert = commit(store, token, 26, tick=26)
        live = cert["liveness"]
        self.assertTrue(live["ok"])
        self.assertEqual(live["dangling"], [])
        self.assertEqual(live["survivors_without_root"], [])
        edge_ids = {e["ref_id"] for e in live["valid_edges"]}
        self.assertEqual(edge_ids, {"R1", "R2", "R3", "R5", "R6"})
        self.assertNotIn("R4", edge_ids)
        for survivor in live["survivors"]:
            self.assertTrue(live["root_reasons"][survivor])

    def test_certificate_replays_and_is_deterministic(self):
        store = build_store()
        token = create_plan(store, 25, tick=25).plan["plan_token"]
        cert = commit(store, token, 26, tick=26)
        replayed = replay_certificate(cert)
        self.assertTrue(replayed["replay"]["verified"])
        self.assertEqual(replayed["certificate_hash"], cert["certificate_hash"])
        # 独立重放同一事件时间线，得到相同证书
        rebuilt = Store.from_events(cert["timeline"][:-1])
        cert2 = commit(rebuilt, cert["plan_token"], 26, tick=26)
        self.assertEqual(cert2["certificate_id"], cert["certificate_id"])
        self.assertEqual(cert2["certificate_hash"], cert["certificate_hash"])

    def test_stale_and_double_commit_rejected(self):
        store = build_store()
        token = create_plan(store, 25, tick=25).plan["plan_token"]
        commit(store, token, 25, tick=25)
        with self.assertRaises(CommitRejected):
            commit(store, token, 29, tick=29)
        p3 = create_plan(store, 29, tick=29)
        p4 = create_plan(store, 29, tick=29)
        with self.assertRaises(CommitRejected):
            commit(store, p3.plan["plan_token"], 29, tick=29)
        cert4 = commit(store, p4.plan["plan_token"], 29, tick=29)
        self.assertEqual(cert4["deleted"], [])

    def test_recompute_after_unfreeze(self):
        store = build_store()
        token = create_plan(store, 25, tick=25).plan["plan_token"]
        commit(store, token, 25, tick=25)
        store.revoke_freeze("F1", tick=27)
        p2 = create_plan(store, 27, tick=28)
        cert = commit(store, p2.plan["plan_token"], 28, tick=28)
        self.assertEqual(sorted(d["node_id"] for d in cert["deleted"]),
                         ["E5@v1", "E6@v1"])
        self.assertTrue(replay_certificate(cert)["replay"]["verified"])

    def test_dangling_reference_rejected(self):
        store = Store()
        store.add_evidence("A", 1, 0, h("A", 1), tick=0)
        with self.assertRaises(Exception):
            store.add_reference("RX", ("A", 1), ("B", 1), tick=1)
        with self.assertRaises(Exception):
            store.add_reference("RY", ("C", 1), ("A", 1), tick=1)

    def test_tick_must_be_monotonic(self):
        store = Store()
        store.add_evidence("A", 1, 0, h("A", 1), tick=5)
        with self.assertRaises(StaleTickError):
            store.add_evidence("B", 1, 0, h("B", 1), tick=4)


class InterleavingTests(unittest.TestCase):
    def test_six_permutations_and_ordering_effect(self):
        results = enumerate_interleavings()
        self.assertEqual(len(results), 6)
        orders = {tuple(r["order"]) for r in results}
        self.assertEqual(len(orders), 6)
        for r in results:
            order = r["order"]
            commit_before_add = order.index("commit") < order.index("add_ref")
            trace_text = "\n".join(r["trace"])
            if commit_before_add:
                self.assertIn("add_ref 被拒绝", trace_text)
                self.assertEqual(r["final_survivors"], ["X@v1"])
            else:
                self.assertIn("阻止[Z@v1]", trace_text)
                self.assertEqual(r["final_survivors"], ["X@v1", "Z@v1"])
            self.assertEqual(r["followup_gen2"]["deleted"], ["Y@v1"],
                             "解冻后 Y 由下一代回收")
        hashes = {r["final_hash"] for r in results}
        self.assertEqual(len(hashes), 2)  # 两种等价类


class CatalogTests(unittest.TestCase):
    def test_stable_ids_and_versions(self):
        for table in (catalog.RULES, catalog.STEPS, catalog.FACT_TYPES):
            for key, meta in table.items():
                self.assertTrue(isinstance(key, str) and key)
                self.assertIsInstance(meta["version"], int)
                self.assertGreaterEqual(meta["version"], 1)

    def test_certificate_carries_id_version_and_rule_meta(self):
        store = build_store()
        token = create_plan(store, 25, tick=25).plan["plan_token"]
        cert = commit(store, token, 25, tick=25)
        self.assertEqual(cert["kind"], catalog.CERTIFICATE_KIND)
        self.assertEqual(cert["fact_type"]["id"], "gc.certificate")
        self.assertEqual(cert["rules"]["R-NEW-1"]["version"],
                         catalog.RULES["R-NEW-1"]["version"])
        for sid in ("S1", "S2", "S3", "S4", "S5"):
            self.assertIn(sid, cert["steps"])
        for d in cert["deleted"]:
            self.assertIn("node_id", d)
            self.assertIn("content_hash", d)
        self.assertTrue(cert["certificate_hash"])


if __name__ == "__main__":
    unittest.main()
