# Transactional Sparse Matrix (stdlib only, Python 3.11+)

CSR 稀疏矩阵，支持事务性切片赋值、历史只读视图与内存额度控制。
不依赖 NumPy/SciPy，尺寸与索引均显式输入。

## 语义

- **规范格式**：每行列索引严格升序；显式零被存储且永不剪枝。
- **切片赋值**：`mat[rows, cols] = values`，`rows`/`cols` 可为 `slice`、
  `int` 或任意非连续索引列表；标量值广播到整个目标块。
- **重复目标**：按行主序应用，后写胜出。
- **重叠赋值**：右侧先物化为稠密快照（`DenseSnapshot`），源与目标共享
  底层存储时按赋值前快照读取。
- **原子性**：越界索引、形状不匹配、内存额度不足都在写入前抛出，
  不留下半更新；`transaction()` 可将多次赋值合并为一个原子单元，
  异常时整体回滚。
- **内存额度**：以行块计。赋值触及的不同行数超过 `memory_quota` 时抛出
  `QuotaExceededError`；实际复制的行块数见 `rows_copied_last` /
  `rows_copied_total`。
- **历史视图**：每次成功提交追加一个不可变版本，`history_view(i)` 返回
  只读视图（`version_count` 为版本总数）。

## 运行样例

```bash
python3 demo.py
```

## 测试

```bash
python3 -m unittest test_sparse_matrix -v
```

测试包含一个自写稠密参考模型（`DenseModel`），以固定种子对随机重叠
赋值做 300 轮对照，并在每轮校验规范格式（列索引有序）。

## 文件

- `sparse_matrix.py` — 实现（`SparseMatrix`、`ReadOnlyView`、`DenseSnapshot`）
- `test_sparse_matrix.py` — unittest 测试套件
- `demo.py` — 运行样例
