# dedup — 纯标准库批量去重引擎（Python 3.11）

按指定键列对分批输入的行去重，支持保留首条/末条、批次更新、哈希碰撞安全。

## 语义约定

- **输入**：表按"带列类型的批次"输入。`Column(name, type)`，`type ∈ {int, float, str, bool}`；
  行是与 schema 等长的元组。所有批次共用同一 schema（`add_batch` 可显式校验）。
- **null**：用 `None` 表示，去重时 null 与 null 相等。
- **NaN**：float 列中的 `float("nan")`，与 null 不同；NaN 与 NaN 视为同组。
- **整数精确**：int 列使用 Python 任意精度整数，超出 float53 的整数也能精确区分。
- **哈希碰撞**：自建 `HashIndex` 仅以哈希分桶，相等性始终比较完整规范化键；
  即使所有键哈希相同也能正确区分值（测试用常量哈希函数验证）。
- **事件序**：`add_batch` 调用顺序 + 批内行顺序构成声明事件序；`keep="last"` 时
  对旧 ID 的更新按此顺序生效。
- **空键列**：`key_columns=[]` 时全表视为同一组。
- **结果顺序**：由被保留行的原始全局序号升序决定。
- 不调用任何现成查询引擎（无 sqlite3/pandas），仅标准库。

## API

```python
from dedup import Column, DedupEngine

schema = [Column("id", "int"), Column("name", "str"), Column("score", "float")]
engine = DedupEngine(schema, key_columns=["id"], keep="last")
engine.add_batch([(1, "alice", 90.0), (2, "bob", None)])
engine.add_batch([(1, "alice", 95.5)])   # 更新 id=1
engine.result()  # [(2, 'bob', None), (1, 'alice', 95.5)]
```

## 运行

```bash
python3.11 dedup.py                 # 演示样例
python3.11 -m unittest -v test_dedup.py   # 测试（25 个用例）
```
