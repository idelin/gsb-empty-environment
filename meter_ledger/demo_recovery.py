"""恢复演示:阶梯模式验证场景 + 崩溃恢复。

场景:采样 (0,2) (10,5) (4,3),查询 A=[0,12) B=[3,9),断测 [6,8),
校准 v1 自 t=5 起 scale=2。校准差量发布到一半(旧结果已撤销、
替换未发布)时模拟崩溃,随后从日志与检查点恢复,验证重放不重复扣减。

运行: python3 demo_recovery.py
"""
import os
import shutil
import sys
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from meterledger import (Consumer, Journal, MeterLedger, load_consumer,
                         write_checkpoint)

HERE = os.path.dirname(os.path.abspath(__file__))
STATE_DIR = os.path.join(HERE, "demo_state")
JOURNAL_PATH = os.path.join(STATE_DIR, "journal.log")
CHECKPOINT_PATH = os.path.join(STATE_DIR, "checkpoint.json")


class SimulatedCrash(Exception):
    pass


class CrashyLedger(MeterLedger):
    """在第一条撤销差量落盘后立即崩溃的账本。"""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.armed = False

    def _emit_delta(self, q, kind, result):
        event = super()._emit_delta(q, kind, result)
        if self.armed and kind == "revoke":
            raise SimulatedCrash(f"{event['delta_id']} 已落盘, 替换结果未发布")
        return event


def show_deltas(title, deltas):
    print(f"\n-- {title} --")
    print(f"  {'pos':>4} {'delta_id':<16} {'查询':<4} {'动作':<5} {'结果':<6} "
          f"{'积分':>5} {'缺测':<4} {'采样v':>5} {'校准v':<5}")
    for d in deltas:
        kind = "撤销" if d["kind"] == "revoke" else "发布"
        print(f"  {d['pos']:>4} {d['delta_id']:<16} {d['query_id']:<4} {kind:<5} "
              f"{d['result_id']:<6} {d['value']:>5} {d['missing']:>4} "
              f"{d['sample_version']:>5} {d['calibration_version']:>5}")


def show_totals(title, consumer):
    print(f"\n-- {title} (位置={consumer.position}, "
          f"确认位置={consumer.confirmed_position}, 去重命中={consumer.duplicates}) --")
    for qid, t in sorted(consumer.totals.items()):
        print(f"  查询 {qid}: 当前结果={t['result_id']} "
              f"积分={t['value']} 缺测={t['missing']}")


def main():
    if os.path.exists(STATE_DIR):
        shutil.rmtree(STATE_DIR)
    os.makedirs(STATE_DIR)

    journal = Journal(JOURNAL_PATH)
    ledger = CrashyLedger(journal, mode="step")

    print("== 阶段 1: 建账(阶梯模式) ==")
    ledger.add_sample(0, 2)
    ledger.add_sample(10, 5)
    ledger.register_query("A", 0, 12)
    ledger.add_sample(4, 3)
    ledger.register_query("B", 3, 9)
    ledger.declare_gap(6, 8)
    show_deltas("阶段 1 差量(撤销/替换与所用版本)", ledger.deltas)

    consumer = Consumer()
    consumer.replay(journal)
    consumer.confirm()
    write_checkpoint(CHECKPOINT_PATH, ledger, consumer)
    show_totals("阶段 1 消费者", consumer)

    print("\n== 阶段 2: 校准自 t=5 生效(scale=2), 发布中途崩溃 ==")
    ledger.armed = True
    try:
        ledger.add_calibration(5, scale=2)
    except SimulatedCrash as exc:
        print(f"  !! 模拟崩溃: {exc}")
    crash_pos = journal.next_pos - 1
    print(f"  日志停在位置 {crash_pos}: 只有撤销, 没有替换")

    print("\n== 阶段 3: 重启恢复 ==")
    recovered = MeterLedger.recover(Journal(JOURNAL_PATH), mode="step")
    show_deltas("恢复补发的差量(稳定 ID, 可去重)", recovered.deltas)

    consumer2 = load_consumer(CHECKPOINT_PATH)
    consumer2.replay(journal, until=crash_pos)
    write_checkpoint(CHECKPOINT_PATH, recovered, consumer2)
    show_totals("消费者重放到崩溃点后又崩溃(撤销已应用)", consumer2)

    consumer3 = load_consumer(CHECKPOINT_PATH)
    consumer3.replay(journal)
    consumer3.confirm()
    write_checkpoint(CHECKPOINT_PATH, recovered, consumer3)
    show_totals("恢复后最终", consumer3)

    assert consumer3.totals["A"]["value"] == Fraction(49)
    assert consumer3.totals["A"]["missing"] == Fraction(2)
    assert consumer3.totals["B"]["value"] == Fraction(17)
    assert consumer3.totals["B"]["missing"] == Fraction(2)
    assert consumer3.duplicates == 1
    assert recovered.queries["A"].value == Fraction(49)
    assert recovered.queries["B"].value == Fraction(17)
    print("\n恢复校验通过: 撤销未重复扣减, 最终结果与账本重算一致。")
    print(f"状态目录: {STATE_DIR}")


if __name__ == "__main__":
    main()
