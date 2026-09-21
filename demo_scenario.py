"""场景验证:阶梯模式 + 断测区间 + 追溯生效的校准。

运行: python demo_scenario.py
"""
import os
import tempfile

from meter_ledger import Ledger


def fmt(result):
    return (
        f"积分={result['integral']:>3} 缺测={result['missing']} "
        f"(采样v{result['sample_version']}, 校准v{result['calibration_version']}, "
        f"确认位置#{result['position']}, id={result['delta_id']})"
    )


def print_deltas(ledger, label, since):
    """把 history 中 [since:] 的撤销/替换差量按查询配对打印。"""
    pending = {}
    for tx, kind, qid, result in ledger.history[since:]:
        if kind == "revoke":
            pending[qid] = result
        elif qid in pending:
            old = pending.pop(qid)
            print(f"  [{label}] {qid} [{result['start']},{result['end']}): "
                  f"撤销 积分={old['integral']} 缺测={old['missing']}"
                  f"(采样v{old['sample_version']},校准v{old['calibration_version']})"
                  f" -> 替换 {fmt(result)}")
        else:
            print(f"  [{label}] {qid} [{result['start']},{result['end']}): "
                  f"发布 {fmt(result)}")


def main():
    tmp = tempfile.mkdtemp(prefix="meter-scenario-")
    ledger = Ledger(os.path.join(tmp, "journal.log"), os.path.join(tmp, "ckpt.json"))

    print("== 阶梯模式:采样 (0,2)、(10,5) ==")
    ledger.add_sample(0, 2)
    ledger.add_sample(10, 5)
    mark = len(ledger.history)
    ledger.register_query("Q1", 0, 12)
    print_deltas(ledger, "注册查询", mark)
    assert ledger.published["Q1"]["integral"] == "30"

    mark = len(ledger.history)
    ledger.add_sample(4, 3)
    print_deltas(ledger, "补采样(4,3)", mark)
    assert ledger.published["Q1"]["integral"] == "36"

    print("== 保留 [0,12) 与 [3,9) 两个查询,加入断测 [6,8) ==")
    mark = len(ledger.history)
    ledger.register_query("Q2", 3, 9)
    print_deltas(ledger, "注册查询", mark)
    mark = len(ledger.history)
    ledger.declare_gap(6, 8)
    print_deltas(ledger, "断测[6,8)", mark)

    print("== 新校准自时刻 5 生效: value' = 2 * value ==")
    mark = len(ledger.history)
    ledger.add_calibration(5, scale=2, offset=0)
    print_deltas(ledger, "校准@5 x2", mark)

    assert ledger.published["Q1"]["integral"] == "49"
    assert ledger.published["Q1"]["missing"] == "2"
    assert ledger.published["Q2"]["integral"] == "17"
    assert ledger.published["Q2"]["missing"] == "2"
    print("全部断言通过。")


if __name__ == "__main__":
    main()
