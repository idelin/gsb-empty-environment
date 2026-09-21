"""检查点:未闭合采样段 + 查询索引 + 去重状态,原子落盘。"""
import json
import os
from fractions import Fraction

from .consumer import Consumer


def write_checkpoint(path, ledger, consumer):
    unclosed = ledger.curve.unclosed_segment()
    data = {
        "type": "checkpoint",
        "confirmed_position": consumer.confirmed_position,
        "unclosed_segment": (
            {"start": str(unclosed["start"]), "value": str(unclosed["value"]),
             "mode": unclosed["mode"]}
            if unclosed is not None else None),
        "query_index": {
            qid: {
                "start": str(q.a),
                "end": str(q.b),
                "result_id": q.current_result().result_id,
                "value": str(q.value),
                "missing": str(q.missing),
                "sample_version": q.sample_version,
                "calibration_version": q.calibration_version,
            }
            for qid, q in sorted(ledger.queries.items())
        },
        "dedup": {
            "applied_delta_ids": sorted(consumer.applied_delta_ids),
            "last_position": consumer.position,
        },
        "consumer_totals": {
            qid: {"value": str(t["value"]), "missing": str(t["missing"]),
                  "result_id": t["result_id"]}
            for qid, t in sorted(consumer.totals.items())
        },
    }
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2, sort_keys=True)
    os.replace(tmp, path)
    return data


def load_consumer(path):
    """从检查点恢复消费者(位置、确认位置、去重状态、累计值)。"""
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    consumer = Consumer()
    consumer.position = data["dedup"]["last_position"]
    consumer.confirmed_position = data["confirmed_position"]
    consumer.applied_delta_ids = set(data["dedup"]["applied_delta_ids"])
    for qid, t in data["consumer_totals"].items():
        consumer.totals[qid] = {"value": Fraction(t["value"]),
                                "missing": Fraction(t["missing"]),
                                "result_id": t["result_id"]}
    return consumer
