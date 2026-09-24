"""运行样例：三版本读写、快照、水位压缩、墓碑回收、null vs NaN、精确整数。"""

import math

from mvcc_store import Batch, ColumnType, MvccStore

SCHEMA = {"balance": ColumnType.INT, "score": ColumnType.FLOAT, "name": ColumnType.TEXT}


def show(store, version, label):
    rows = store.scan(version)
    print(f"  {label} (v{version}): {rows}")


def main():
    store = MvccStore(SCHEMA)

    print("== 1. 三版本写入 ==")
    v1 = store.apply_batch(Batch.of(SCHEMA, [
        {"id": 1, "balance": 5, "score": 1.5, "name": "alice"},
        {"id": 2, "balance": 100, "score": 2.5, "name": "bob"},
    ]))
    v2 = store.apply_batch(Batch.of(SCHEMA, [
        {"id": 1, "balance": 7, "score": 1.5, "name": "alice"},
    ]))
    v3 = store.apply_batch(Batch.of(SCHEMA, []), deletes=[1])
    print(f"  v1=插入(行1 balance=5), v2=改(行1 balance=7), v3=删行1 -> 版本 {v1},{v2},{v3}")

    print("== 2. 三个版本各自查询 ==")
    print(f"  v1 行1 balance = {store.read(1, 'balance', 1)}")
    print(f"  v2 行1 balance = {store.read(1, 'balance', 2)}")
    print(f"  v3 行1 存在?   = {store.row_exists(1, 3)}")
    show(store, 1, "v1 全表")
    show(store, 2, "v2 全表")
    show(store, 3, "v3 全表")

    print("== 3. 活跃快照2存在时压缩，不能丢7 ==")
    with store.snapshot(2) as snap:
        print(f"  压缩前层数={len(store.layers)}, 水位={store.watermark}")
        stats = store.compact()
        print(f"  压缩结果: {stats}")
        print(f"  快照2 读行1 balance = {snap.get(1, 'balance')}  (仍为7)")
        print(f"  当前版本行1存在? {store.row_exists(1)}  (删除层在水位之上，未受影响)")
    print(f"  快照释放后水位={store.watermark}")
    stats = store.compact()
    print(f"  再次压缩: {stats}  (墓碑被回收)")
    show(store, 3, "压缩后当前全表")

    print("== 4. null 与 NaN 不同 ==")
    s2 = MvccStore(SCHEMA)
    s2.apply_batch(Batch.of(SCHEMA, [
        {"id": 1, "balance": None, "score": None, "name": "null"},
        {"id": 2, "balance": 0, "score": float("nan"), "name": "nan"},
    ]))
    _, null_val = s2.read(1, "score")
    _, nan_val = s2.read(2, "score")
    print(f"  行1 score = {null_val!r} (null), 行2 score = {nan_val!r} (NaN)")
    print(f"  null is None: {null_val is None}, NaN isnan: {math.isnan(nan_val)}, 两者相等? {null_val == nan_val}")

    print("== 5. 整数精确计算 ==")
    s3 = MvccStore(SCHEMA)
    big1, big2 = 2**70 + 12345, 2**69
    s3.apply_batch(Batch.of(SCHEMA, [
        {"id": 1, "balance": big1, "score": 0.0, "name": "x"},
        {"id": 2, "balance": big2, "score": 0.0, "name": "y"},
    ]))
    total = s3.sum_column("balance")
    print(f"  {big1} + {big2} = {total}")
    print(f"  精确? {total == big1 + big2}, 类型: {type(total).__name__}")


if __name__ == "__main__":
    main()
