"""本地夹具：订单（左）与发货（右）的 CDC 变更 + 水位标记。

不连接任何消息平台——所有输入都是本文件中的静态数据。
列表元素为 ``(事件, 说明)``，按提交序号（seq）严格有序排列。
连接键为订单号 ``oid``，事件时间列为 ``ts``，容差 5（闭区间）。
"""

from __future__ import annotations

from event_join import Change, Watermark

ORDERS = "orders"
SHIPMENTS = "shipments"
TOLERANCE = 5
KEY_COL = "oid"
TS_COL = "ts"


def ins(seq: int, table: str, pk: str, oid: str, ts: int):
    return Change(seq, table, pk, None, {KEY_COL: oid, TS_COL: ts})


def dele(seq: int, table: str, pk: str, oid: str, ts: int):
    return Change(seq, table, pk, {KEY_COL: oid, TS_COL: ts}, None)


def upd(seq: int, table: str, pk: str, oid1: str, ts1: int,
        oid2: str, ts2: int):
    return Change(
        seq,
        table,
        pk,
        {KEY_COL: oid1, TS_COL: ts1},
        {KEY_COL: oid2, TS_COL: ts2},
    )


def wm(seq: int, ts: int) -> Watermark:
    return Watermark(seq, ts)


# (事件, 说明)
EVENTS: list[tuple] = [
    # ---- 场景 1：一左多右；恰容差边界包含；超界不连接 ----
    (ins(1, ORDERS, "O1", "A", 100), "左：订单 O1（A @100）"),
    (ins(2, SHIPMENTS, "S1", "A", 105), "右：发货 S1（A @105，|dt|=5 恰边界→连接）"),
    (ins(3, SHIPMENTS, "S2", "A", 104), "右：发货 S2（A @104→连接，一左多右）"),
    (ins(4, SHIPMENTS, "S3", "A", 106), "右：发货 S3（A @106，|dt|=6 超界→不连接）"),
    (ins(5, ORDERS, "O9", "Z", 200), "左：订单 O9（键 Z，无关键，留存状态）"),

    # ---- 场景 2：最终化之前撤回 ----
    (ins(10, ORDERS, "O2", "B", 100), "左：订单 O2（B @100）"),
    (ins(11, SHIPMENTS, "S4", "B", 102), "右：发货 S4（B @102→连接）"),
    (dele(12, ORDERS, "O2", "B", 100), "左：删除 O2（配对尚未最终化→撤回 O2|S4）"),

    # ---- 场景 3：水位推进、状态清理与最终化 ----
    (wm(20, 104), "水位→104（O1 需 wm>=105 才封闭，暂不最终化）"),
    (wm(21, 105), "水位→105（O1 到期冻结；配对须等较晚一侧 max(ts)+5）"),
    (wm(22, 109), "水位→109（S2 到期：max(100,104)+5=109→O1|S2 最终化）"),
    (wm(23, 110), "水位→110（S1 到期：max(100,105)+5=110→O1|S1 最终化）"),
    (wm(24, 111), "水位→111（S3 无配对，直接清理）"),

    # ---- 场景 4：清理后的迟到 / 越界事件单独报告，结果不被静默重建 ----
    (ins(30, ORDERS, "O_LATE", "A", 100), "迟到插入 A@100（ts<=wm 111→旁路报告）"),
    (dele(31, SHIPMENTS, "S1", "A", 105), "迟到删除已清理的 S1（已最终化结果保持不变）"),
    (dele(32, SHIPMENTS, "S_GHOST", "A", 100), "删除从未存在的 S_GHOST（越界报告）"),
    (Change(33, "audit", "X1", None, {KEY_COL: "A", TS_COL: 111}),
     "非左/右表 audit 的变更（旁路报告，不处理）"),

    # ---- 场景 5：配对 ID 去重（重放幂等 + 更新修正轨迹） ----
    (wm(40, 112), "水位→112"),
    (ins(41, ORDERS, "O7", "C", 300), "左：订单 O7（C @300）"),
    (ins(42, SHIPMENTS, "S7", "C", 302), "右：发货 S7（C @302→连接）"),
    (ins(43, ORDERS, "O7", "C", 300), "重复投递 O7（同键同时间→幂等，不产生新配对事件）"),
    (upd(44, ORDERS, "O7", "C", 300, "C", 301),
     "更新 O7：C@300→C@301（撤旧建新，最终仍是同一个配对 ID）"),
    (ins(41, ORDERS, "O7", "C", 300), "重放 seq=41（提交序号重复→忽略）"),
]
