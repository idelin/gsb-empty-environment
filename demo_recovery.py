"""恢复演示:校准事务在"旧结果已撤销、新结果尚未发布"处崩溃。

事务差量按 revoke(Q1), revoke(Q2), publish(Q1), publish(Q2) 的顺序
落盘。我们在 publish(Q1) 之后模拟崩溃,此时 Q2 处于"已撤销未发布"。
重启后:
  1. 从 checkpoint 的确认位置重放 journal;
  2. 已应用差量按稳定 ID 去重,不会重复扣减;
  3. 未提交事务按相同稳定 ID 补发缺失的 publish(Q2) 并提交;
  4. 再次全量重放,结果一致。

运行: python demo_recovery.py
"""
import os
import tempfile

from meter_ledger import Ledger, SimulatedCrash


def main():
    tmp = tempfile.mkdtemp(prefix="meter-recovery-")
    journal = os.path.join(tmp, "journal.log")
    ckpt = os.path.join(tmp, "ckpt.json")

    print("== 阶段 1:建立历史并 checkpoint ==")
    ledger = Ledger(journal, ckpt)
    ledger.add_sample(0, 2)
    ledger.add_sample(10, 5)
    ledger.add_sample(4, 3)
    ledger.register_query("Q1", 0, 12)
    ledger.register_query("Q2", 3, 9)
    ledger.declare_gap(6, 8)
    ledger.checkpoint()
    print(f"  checkpoint 已写入,确认位置 #{ledger.position}, "
          f"已应用差量 {len(ledger.applied_deltas)} 条")

    print("== 阶段 2:校准@5 事务,在 publish(Q1) 后崩溃 ==")
    crashed = Ledger(journal, ckpt)
    seen = []

    def hook(delta_id, kind, qid):
        seen.append(delta_id)
        if kind == "publish":
            raise SimulatedCrash(f"崩溃于 {delta_id} 已落盘之后")

    crashed.delta_hook = hook
    try:
        crashed.add_calibration(5, scale=2, offset=0)
    except SimulatedCrash as exc:
        print(f"  {exc}")
    print(f"  崩溃前已落盘差量: {seen}")
    del crashed

    print("== 阶段 3:重启,重放并完成未提交事务 ==")
    recovered = Ledger(journal, ckpt)
    for line in recovered.recovery_log:
        print(f"  {line}")
    for qid in ("Q1", "Q2"):
        r = recovered.published[qid]
        print(f"  {qid}: 积分={r['integral']} 缺测={r['missing']} "
              f"(采样v{r['sample_version']}, 校准v{r['calibration_version']}, "
              f"确认位置#{r['position']})")
    assert recovered.published["Q1"]["integral"] == "49"
    assert recovered.published["Q2"]["integral"] == "17"
    recovered.checkpoint()

    print("== 阶段 4:再次重启全量重放,验证无重复扣减 ==")
    replayed = Ledger(journal, ckpt)
    assert replayed.published == recovered.published
    n_ids = len(replayed.applied_deltas)
    print(f"  二次重放结果一致;去重集合 {n_ids} 条,无重复应用")
    print("恢复演示完成。")


if __name__ == "__main__":
    main()
