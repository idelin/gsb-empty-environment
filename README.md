# 段落锚点重定位（anchor_relocate）

文本视为 **Unicode 码点序列**：所有偏移、长度均为码点语义（Python `str` 的
`len`/索引），绝不使用 UTF-8 字节长度。纯 Python 3.11 标准库，无第三方依赖。

## 运行

```sh
python3.11 anchor_relocate.py            # 从 stdin 读 JSON，结果写 stdout
python3.11 anchor_relocate.py input.json # 或从文件读
python3.11 -m unittest -v                # 运行测试
```

退出码：`0` 成功；`2` 输入非法；`3` 编辑重叠（整批原子拒绝，stdout 无输出，
错误 JSON 写 stderr）。

## 输入格式（JSON）

```json
{
  "text": "基准版本文本",
  "anchors": [{"name": "a1", "pos": 2, "affinity": "left"}],
  "changes": [
    [{"start": 2, "delete": 0, "text": "插入内容"}],
    [{"start": 9, "delete": 5, "text": "世界"}]
  ]
}
```

- `text`：基准版本（v0）文本。
- `anchors`：锚点列表。`pos` 为基准版本码点偏移（`0..len(text)`）；
  `affinity` 为 `left`（默认）或 `right` 吸附；`name` 可选。
- `changes`：变更集列表，每个变更集是一批编辑，**以该批次应用前的版本
  偏移定义**，按顺序依次应用得到当前版本。
- 编辑 `{start, delete, text}`：删除 `[start, start+delete)` 并插入 `text`。
  `delete=0` 为插入，`text=""` 为删除，两者皆有为替换。同批编辑区间
  （`[start, start+delete)`）不得重叠，否则整批原子拒绝；相邻（端点相接）
  允许。

## 输出格式（JSON）

```json
{
  "text": "当前版本文本",
  "anchors": [
    {"name": "a1", "pos": 2, "affinity": "left", "covered": false, "reasons": []}
  ]
}
```

- `pos`：锚点在当前版本中的码点偏移；被覆盖时为折叠位置。
- `covered` / `reasons`：映射途中是否被删除/替换区间覆盖；原因取值为
  `covered-by-delete` 或 `covered-by-replace`，跨多版本时按发生顺序累积。

## 映射规则

对编辑 `[start, end)`（`end = start + delete`，替换为 `inserted` 个码点）：

| 锚点位置 | left 吸附 | right 吸附 |
| --- | --- | --- |
| `p < start` | 不变 | 不变 |
| `p > end` | `p + inserted - delete` | 同左 |
| 纯插入且 `p == start` | 不变（验收：位置 2 插 3 码点 → 仍为 2） | `start + inserted`（验收：→ 5） |
| `start < p < end` | 覆盖，折叠到 `start` | 同左 |
| `p == start` 且 `delete > 0` | 幸存，留在 `start` | 覆盖，折叠到 `start` |
| `p == end` 且 `delete > 0` | 覆盖，折叠到 `start + inserted` | 幸存，映射到 `start + inserted` |

被覆盖的锚点以折叠位置继续参与后续版本映射，`covered` 与原因保留。

## 文件

- `anchor_relocate.py`：核心库 + CLI（`Edit` / `Anchor` / `apply_edits` /
  `map_anchor_through_batch` / `apply_changes` / `map_anchor` / `run` / `main`）。
- `test_anchor_relocate.py`：unittest，含验收边界（位置 2 插 3 码点、
  UTF-8 字节长度不可替代码点长度）、重叠原子拒绝、跨版本映射、CLI 测试。
- 所有示例与测试数据均为合成数据。
