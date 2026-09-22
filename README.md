# 大 DFA 的页式压缩转移表

纯 Python 3.11 + 标准库实现（不使用任何正则/自动机库）。大 DFA 的转移表用
**默认目标 + 区间例外 + 行共享**压缩，再按“每页固定符号数”切片；只读查询按需
解码单页，补丁只重建变化页并生成**可逆页差量**，旧视图靠引用固定继续工作。

## 压缩模型

* 每行：`default`（最高频目标，同频取最小 id）+ 若干 `(start, length, target)`
  区间例外，坐标是排序后字母表中的位置。
* **显式死转移**用哨兵 `DEAD = -1` 表示，和普通状态 id 同等编码。默认目标永远
  不会掩盖死转移：混合行里的死格一定以显式区间出现（见
  `test_default_does_not_mask_explicit_dead`）。
* **行共享**：压缩后完全相同的行去重，`row_of[state]` 指向唯一行。
* **分页**：每 `page_size` 个符号一页。跨页区间在编码时按页边界切分，页内片段
  不越界；因此一次查询只读覆盖该符号的一页。
* 页是“整页切片”：含该符号区间上所有行的片段。补丁页额外携带私有别名的覆盖
  片段，未触碰的页字节原样复用。

## 容器格式（不可变字节串）

```
[ header1 ][ header2 ][ toc ][ index_digest16 ][ page0 ][ page1 ] ...
```

* `header1`（稳定盐基）：魔数/版本、页大小、字母表、状态数、接受集、去重行、
  状态→行映射。
* `header2`（版本区）：8 字节随机 tag + 别名向量。补丁换版本不必重写未改页。
* `toc`：每页 `(offset, length, digest)`，页区域不得交叠/越界。
* `index_digest` = BLAKE2b(`header1||header2||toc`)，**开 View 前强制验证**。
* 每页：`page_id | entry_count | entries | checksum16`，校验和以
  `BLAKE2b(header1) || page_id` 为密钥——页体无法跨页/跨镜像搬移。
* 每个例外的目标状态在页加载时再次校验；非状态非 `DEAD` 立即拒绝。

## 解码门禁（先验证再查询）

1. `parse_index` 开库即验证：魔数、版本、字母表严格有序、行/别名/状态引用、
   区间顺序与边界、接受集、TOC 偏移/长度/交叠/越界、索引摘要。
2. 首次触碰某页时 `parse_page` 验证：TOC 全量摘要、密钥校验和、嵌入页号、
   区间边界、**每个目标状态**。全部通过后才返回片段。

## 故障分流（分别处理）

| 情形 | 异常 | 行为 |
| --- | --- | --- |
| 跨页符号范围 | 编码自动切分，页片段不越界 | 查询只读所在页 |
| 损坏偏移（TOC） | `IntegrityError` | 开库即失败，无法查询 |
| 损坏页体/校验和 | `InvalidPage` | 该页加载失败并记忆，不计入读页数，其他页正常 |
| 缺失页 | `MissingPage` | fetcher 抛出，记忆为缺失，其他页正常 |
| 取消加载 | `PageLoadCancelled` | 不缓存任何内容，`view.retry(pid)` 后可重试 |

## 可逆补丁与热替换

* `Edit(state, symbol, target)`，目标可为状态或 `DEAD`。
* **写时复制**：被改状态获得私有“别名行”（base = 它当前解析到的行），只在
  被触碰页上覆盖。修改一个共享行绝不污染共享它的其他状态。
* `build_patch` 只读取/校验被触碰的页；返回 `Delta(old/new page blobs,
  old/new header2, edits)`。
* `apply_delta(blob, delta, forward=True/False)`：正向得到新镜像，逆向**字节级**
  还原旧镜像；差量自身带摘要，基线不匹配时拒绝应用。
* `VersionStore`：`commit` 原子切换 `current`；经 `open_view` 打开的视图 pin 住
  其版本字节，关闭前旧版本不会被 GC——旧视图始终查询旧表。

## 布局

```
paged_dfa/
  wire.py    变长整数、BLAKE2b 摘要、有界游标
  model.py   DFA/Row、默认目标+区间例外压缩、行共享、语言枚举
  codec.py   分页、跨页切分、编码、索引/页严格校验
  view.py    只读按需分页视图、缺页/取消/损坏分流、读页统计
  patch.py   COW 别名、区间手术、可逆页差量、版本存储
tests/test_paged_dfa.py   31 个 unittest 用例
examples/demo.py          独立可运行样例
```

## 运行

```bash
python3 examples/demo.py
python3 -m unittest discover -s tests -v
```

## 验证覆盖

* 完整表**逐格对照**压缩查询（含随机 DFA、跨页区间）。
* 死转移不被默认掩盖；行共享后改一行不污染另一行。
* 补丁前后逐格一致；补丁前/后/逆补丁后**语言**一致；逆补丁字节还原。
* 损坏偏移、损坏页、缺失页、取消加载分别报错。
* `pages_read` 统计**实际解码页数**（多点查询不做全解压），补丁仅读取触碰页。
