# 分区分块窗口引擎（纯标准库）

Python 3.11+，仅用标准库，不调用任何现成查询引擎。

## 文件

- `window.py` — 引擎实现（批次输入、排序、peer 组、ROWS/RANGE 帧、RANK/SUM、流式分块）
- `test_window.py` — unittest 测试（21 例）
- `demo.py` — 运行样例

## 运行

```sh
python3 -m unittest test_window -v   # 测试
python3 demo.py                      # 样例
```

## 语义约定

- 输入为 `Batch(columns, rows)`：列带类型（`int`/`float`/`str`），行内 `None` 即 NULL，逐行校验类型。
- `OrderKey(column, desc, nulls_first)`：NULL 位置显式指定且与升/降序无关；
  NaN 与 NULL 不同，视为"最大普通值"（升序最后、降序最前），NaN 与 NaN 互为 peer。
- `RANK()`：重复排序值构成 peer 组，组内并列、组后跳号（键 1,1,2 → 1,1,3）。
- `SUM(col, Frame(mode, start, end))`：
  - `ROWS`：按物理行数，`CURRENT ROW` 只含自身；支持 `N PRECEDING/FOLLOWING`。
  - `RANGE`：按 peer 组，`CURRENT ROW` 含整个 peer 组（两个键 1 都计入）；
    数值偏移帧要求单一升序数值排序键；NULL/NaN 键的偏移帧只覆盖自身 peer 组。
  - SUM 忽略 NULL，全 NULL（或空帧）得 NULL；NaN 在求和中传播。
- 整数精确计算：int 任意精度（如 2**63+1），出现浮点才退化为 float。
- 分块：同一分区的行须连续到达（可跨分块），新分区键出现时结算上一分区，
  分块边界不会截断分区；`finish()` 结算尾部分区。
