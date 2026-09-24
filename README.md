# MVCC 列式增量存储（纯标准库）

Python 3.11，仅用标准库，不调用任何现成查询引擎。

## 设计

- **增量层（DeltaLayer）**：每次批量写入/删除产生一个新版本层，按行 ID
  记录列更新（`updates[row_id][col]`）或墓碑（`deletes`）。
- **快照读**：`scan_at(version)` 只应用版本号 `<= 快照版本` 的层，
  每行每列取该版本之前的最新值；`open_snapshot()` 注册活跃快照。
- **墓碑（TOMBSTONE）**：删除只是写一层标记，旧版本数据保留，
  老快照仍能读到删除前的值。
- **回收水位（gc_watermark）**：最老活跃快照的版本（无快照时为当前版本）。
- **压缩（compact）**：水位及以下的层折叠为一个基线层（版本=水位），
  其中被墓碑覆盖的行物理回收；水位以上的层原样保留。因此旧快照的
  可见集逐版本不变，绝不会看到比它新的值。

## 类型语义

- 输入为带列类型的 `Batch`（INT/FLOAT/STRING/BOOL），写入时逐值校验。
- `null`（None）与 `NaN` 是不同值，`values_equal` 区分二者。
- INT 列拒绝 float/bool，Python int 任意精度，整数计算精确。

## 运行

```sh
python3.11 demo.py                 # 运行样例
python3.11 -m unittest test_mvcc_store -v   # 18 个测试
```
