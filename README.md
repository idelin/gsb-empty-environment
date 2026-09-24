# MVCC 列存引擎（Python 3.11，纯标准库）

## 设计
- **增量层**：每次 `apply_batch` 产生一个 `DeltaLayer(version, updates)`，
  结构为 `列 -> {行ID: 值 | TOMBSTONE}`，层按版本升序排列。
- **快照读**：快照固定版本号；读取时从新到旧扫描，取第一个 `version <= 读版本`
  的条目；命中 `TOMBSTONE` 视为行已删除。
- **水位压缩**：`watermark = min(活跃快照版本)`（无快照时为当前版本）。
  `compact()` 把水位及以下的层合并为基底层，每个 `(列, 行ID)` 只保留
  `<= 水位` 的最新条目——旧快照因此永远看不到新值；基底层中的墓碑被丢弃，
  完成版本回收。水位以上的层原样保留。
- **null vs NaN**：null 为 `None`，NaN 为 `float('nan')`，`values_equal`
  严格区分（None≠NaN，NaN=NaN）。
- **整数精确**：INT 列用 Python `int`（任意精度），求和精确；FLOAT 列求和用 `math.fsum`。
- 不依赖任何现成查询引擎，仅标准库。

## 运行
```sh
python3.11 demo.py                    # 运行样例
python3.11 -m unittest test_mvcc_store -v   # 21 个测试
```

## 文件
- `mvcc_store.py` — 引擎实现
- `test_mvcc_store.py` — unittest 测试
- `demo.py` — 运行样例
