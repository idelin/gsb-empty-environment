# external-sort

外存归并排序（Python 3.11，仅标准库，无第三方依赖、不调用现成查询引擎）。

## 特性

- 表按带列类型的 `Batch` 输入（`int` / `float` / `str` / `bool`，均可为 null）
- 可配置内存额度 `max_rows_in_memory`：超出即排序并溢写临时归并段；
  额度小于单个批次时按块切分溢写，仍可处理
- 混合升降序排序键，null 位置显式指定（`nulls_first`），不随方向翻转
- 稳定排序：相同排序键保持原输入顺序（内部附加全局序号决胜）
- `None`（null）与 `NaN` 不同：NaN 是普通值，排在所有非 NaN 数值之后
- 整数精确比较，绝不转为浮点
- 失败（含注入的归并读错误）时清理临时文件且不产生冒充成功的部分输出；
  归并段文件带结束哨兵，截断会被检测为 `IOError`
- `cancel()` 取消后排序器自动复位，可再次使用

## 用法

```python
from external_sort import Batch, ExternalSorter, Schema, SortKey

schema = Schema([("id", "int"), ("score", "float")])
keys = [SortKey("score", ascending=False, nulls_first=False), SortKey("id")]
sorter = ExternalSorter(schema, keys, max_rows_in_memory=4)
sorter.add_batch(Batch(schema, [(1, 2.0), (2, None), (3, float("nan"))]))
for batch in sorter.sort():
    print(batch.rows)
```

## 运行

```sh
python3.11 external_sort.py            # 演示
python3.11 -m unittest test_external_sort -v   # 测试
```
