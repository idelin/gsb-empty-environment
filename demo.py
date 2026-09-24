"""运行样例：三版本场景 + 活跃快照下的压缩 + null/NaN + 精确整数。"""

from mvcc_store import Batch, ColumnSpec, ColumnType, MVCCStore, Snapshot

schema = [ColumnSpec("v", ColumnType.INT), ColumnSpec("x", ColumnType.FLOAT)]
store = MVCCStore(schema)


def b(rows):
    return Batch(schema, rows)


print("== 写入三个版本 ==")
v1 = store.apply_batch(b([{"_row_id": 1, "v": 5, "x": None}]))
v2 = store.apply_batch(b([{"_row_id": 1, "v": 7, "x": float("nan")}]))
v3 = store.delete_rows([1])
print(f"v1={v1} 写入 v=5, x=null | v2={v2} 改 v=7, x=NaN | v3={v3} 删除行 1")

print("\n== 三版本各自查询 ==")
print("v1 视图:", store.scan_at(v1))
print("v2 视图:", store.scan_at(v2))
print("v3 视图:", store.scan_at(v3), "(行已被墓碑删除)")

print("\n== null 与 NaN 不同 ==")
r1, r2 = store.get_at(1, v1), store.get_at(1, v2)
print(f"v1 的 x 是 null: {r1['x']!r}; v2 的 x 是 NaN: {r2['x']!r}; 两者不相等")

print("\n== 活跃快照 2 存在时压缩 ==")
snap2 = Snapshot(store, v2)
store._snapshots.append(snap2)  # 注册为活跃快照（等价于在 v2 时刻 open）
print("回收水位 =", store.gc_watermark())
print("压缩前层版本:", [l.version for l in store.layers])
store.compact()
print("压缩后层版本:", [l.version for l in store.layers])
print("快照 2 仍读到:", snap2.get(1), "（7 没丢，也看不到 v3 的删除）")
print("最新版本读到:", store.get_at(1, v3), "（删除仍然生效）")
snap2.close()

print("\n== 无快照后再次压缩：墓碑回收 ==")
store.compact()
print("压缩后层:", store.layers, "（已删除行被物理回收）")

print("\n== 整数精确计算 ==")
big = 2**63 + 12345
store.apply_batch(b([{"_row_id": 2, "v": big}]))
got = store.get_at(2, store.current_version)["v"]
print(f"写入 {big}，读出 {got}，类型 {type(got).__name__}，精确相等: {got == big}")
