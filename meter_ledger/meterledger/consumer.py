"""差量消费者:按序应用撤销/发布,去重状态保证重放不重复扣减。"""
from fractions import Fraction


class ProtocolError(Exception):
    """差量序列违反协议(撤销与当前结果不匹配等)。"""


class Consumer:
    def __init__(self):
        self.totals = {}              # qid -> {value, missing, result_id}
        self.applied_delta_ids = set()  # 去重状态
        self.position = 0             # 已看到的日志位置
        self.confirmed_position = 0   # 确认位置
        self.duplicates = 0           # 去重命中次数

    def apply(self, event):
        self.position = max(self.position, event.get("pos", 0))
        if event.get("type") != "delta":
            return False
        delta_id = event["delta_id"]
        if delta_id in self.applied_delta_ids:
            self.duplicates += 1
            return False
        qid = event["query_id"]
        cur = self.totals.setdefault(
            qid, {"value": Fraction(0), "missing": Fraction(0), "result_id": None})
        if event["kind"] == "revoke":
            if cur["result_id"] != event["result_id"]:
                raise ProtocolError(
                    f"撤销 {event['result_id']} 与当前结果 {cur['result_id']} 不匹配")
            cur["value"] -= Fraction(event["value"])
            cur["missing"] -= Fraction(event["missing"])
            cur["result_id"] = None
        else:
            if cur["result_id"] is not None:
                raise ProtocolError(
                    f"发布 {event['result_id']} 时旧结果 {cur['result_id']} 未撤销")
            cur["value"] += Fraction(event["value"])
            cur["missing"] += Fraction(event["missing"])
            cur["result_id"] = event["result_id"]
        self.applied_delta_ids.add(delta_id)
        return True

    def replay(self, journal, until=None):
        """从上次位置继续消费;until 用于演示中模拟中途崩溃。"""
        for event in journal.read_from(self.position + 1):
            if until is not None and event["pos"] > until:
                break
            self.apply(event)

    def confirm(self):
        """确认位置:到此为止的差量都已生效。"""
        self.confirmed_position = self.position
