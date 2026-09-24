# external_sort — 可配置内存额度的外部归并排序

纯标准库实现（Python 3.11+），不依赖任何现成查询引擎。表以**带列类型的批次**
（`Batch` + `Column`/`ColumnType`）输入，在内存额度内排序并溢写为临时归并段，
最后做 k 路归并输出有序行流。

## 特性

- **可配置内存额度**：按行估算内存，达到 `mem_budget` 即溢写一个有序归并段；
  额度小于一个批次（甚至小于一行，如 `mem_budget=1`）也能正常处理。
- **混合升降序**：每个 `SortKey` 独立指定 `ascending`。
- **null 位置**：每个键独立指定 `nulls_first` / `nulls_last`，与升降序无关（SQL 语义）。
- **null ≠ NaN**：`None` 是 null，按 null 位置摆放；`NaN` 是真实浮点值，
  升序时排在所有非 null 数值之后（降序则最前）。
- **整数精确**：Python 任意精度 int 直接比较，大整数（如 `2**80`）不丢失精度。
- **稳定排序**：全局递增序号打破并列，相同排序键保持原输入顺序（跨溢写与归并均成立）。
- **失败清理**：任何异常（I/O 错误、归并段损坏/截断、取消）都会删除临时文件并重置状态。
- **取消可复用**：`cancel()` 可在任意线程调用；取消或失败后 sorter 自动复位，可立即再次使用。
- **不冒充成功**：归并读错误（含注入故障、截断文件）会向调用方抛异常；
  归并段头部记录行数，截断可被检测，绝不会静默返回部分结果。

## 用法

```python
from external_sort import Batch, Column, ColumnType, ExternalSorter, SortKey

schema = (Column("id", ColumnType.INT),
          Column("score", ColumnType.FLOAT),
          Column("name", ColumnType.STRING))

sorter = ExternalSorter(
    schema,
    [SortKey("score", ascending=False, nulls_first=False),
     SortKey("name",  ascending=True,  nulls_first=True)],
    mem_budget=300,          # 字节；小于一个批次也可以
    temp_dir="/tmp",         # 可选
)
sorter.add_batch(Batch(schema, [(1, 9.5, "amy"), (2, None, "bob")]))
sorter.add_batch(Batch(schema, [(3, 7.0, "cy")]))
rows = sorter.sort_to_list()   # 或流式消费 sorter.sort()
```

## 运行

```bash
python3.11 demo.py            # 运行样例
python3.11 -m unittest -v     # 运行测试（16 个用例）
```

## 设计要点

- 比较器 `_compare_rows` 同时用于内存排序（`cmp_to_key`）与归并堆，保证语义一致。
- 归并段为 pickle 记录序列，段头含 magic 与记录数；读出数量不符即报 `SortError`。
- 临时文件放在 `tempfile.mkdtemp` 创建的专属目录，`finally` 中整体删除。
- 堆元素为 `(行比较键, 全局序号, 行, 迭代器)`；序号唯一，稳定且不会比较到迭代器。

## 测试结果

```
Ran 16 tests in 0.240s
OK
```
