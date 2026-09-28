# 段落锚点重定位

文本是 **Unicode 码点序列**；位置是码点之间的间隙，合法范围 `0..len(text)`。
所有偏移与长度均以**码点**计算（`len(str)`），绝不使用 UTF-8 字节长度——
例如 `😀`（U+1F600）是 1 个码点、4 个 UTF-8 字节。

仅依赖 Python 3.11 标准库。

## 语义

- **左吸附（`left`）**：锚点依附其左侧码点。插入发生在锚点间隙时，锚点留在插入内容**之前**。
- **右吸附（`right`）**：锚点依附其右侧码点。插入发生在锚点间隙时，锚点越过插入内容，落在**之后**。
- **删除覆盖**：被删码点所依附的锚点折叠到删除区域对应的间隙，并在 `events` 中记录原因 `deleted`。
  - 左吸附锚点 p 被 `[s,e)` 覆盖 ⇔ `s < p <= e`，折叠到该编辑插入内容的起点；
  - 右吸附锚点 p 被 `[s,e)` 覆盖 ⇔ `s <= p < e`，折叠到插入内容的终点；
  - 端点锚点依附于未删除内容时不算覆盖。
- **替换** = 删除 + 插入，折叠事件的 `edit_kind` 为 `replace`，并带 `inserted_length`。
- **批量编辑原子性**：一批编辑以同一基准版本的偏移给出。区间严格相交（共享内部码点）即拒绝整批，
  不产生新版本；端点相接（`[0,2)` 与 `[2,4)`）合法，同一位置的多个纯插入也合法。

## 命令行

```sh
python3.11 -m anchor_relocation --pretty < request.json
```

从标准输入读取一个 JSON 对象，向标准输出写 JSON 对象。退出码：
`0` 成功；`1` 输入/JSON 或锚点错误；`2` 编辑非法或编辑重叠。

### 输入格式

```json
{
  "initial_text": "abcdef",
  "changes": [
    {
      "edits": [
        {"start": 2, "end": 2, "new_text": "XYZ"}
      ]
    }
  ],
  "anchors": [
    {"id": "L", "position": 2, "gravity": "left",  "version": 0},
    {"id": "R", "position": 2, "gravity": "right", "version": 0}
  ]
}
```

- `initial_text`：初始版本（版本号 0）文本，省略视为空串。
- `changes`：按顺序应用的变更列表；每个变更含一批以**当时最新版本**为基准的编辑。
- 编辑对象：`start`/`end` 为基准版本上的半开码点区间；`end` 省略时等于 `start`（纯插入）；
  `new_text` 省略视为空串（纯删除）。
- `anchors`：待映射锚点。`version` 省略视为 0（旧版本锚点会逐版本映射到当前版）；
  `id` 省略时自动生成；`gravity` 为 `left` 或 `right`。

所有变更先原子应用，之后才计算锚点；任一变更失败则整个请求失败、无部分结果。

### 输出格式

成功：

```json
{
  "status": "ok",
  "initial_text": "abcdef",
  "final_text": "abXYZcdef",
  "version": 1,
  "anchors": [
    {"id": "L", "position": 2, "gravity": "left",  "version": 1, "collapsed": false, "events": []},
    {"id": "R", "position": 5, "gravity": "right", "version": 1, "collapsed": false, "events": []}
  ]
}
```

- `position`：当前版本上的码点偏移。
- `collapsed`：历史上是否曾被删除/替换覆盖。
- `events`：每次折叠的原因链，元素含
  `reason`（`deleted`）、`edit_kind`（`delete`/`replace`）、
  `edit_index`（批内下标）、`base_start`、`base_end`、`inserted_length`、`version`。

失败（重叠，退出码 2）：

```json
{
  "error": {
    "code": "overlapping_edits",
    "message": "第 0 与第 1 个编辑重叠（编辑 1 区间 [3, 5) 与活动编辑相交）",
    "change_index": 0,
    "edit_pair": [0, 1]
  }
}
```

其他错误码：`invalid_json`、`invalid_input`、`invalid_edit`、`invalid_anchor`。

## Python API

```python
from anchor_relocation import TextHistory, Edit, Gravity

history = TextHistory("abcdef")
history.apply([Edit(2, 2, "XYZ")])           # 版本 0 -> 1
history.locate(2, Gravity.LEFT, version=0)   # position=2, collapsed=False
history.locate(2, Gravity.RIGHT, version=0)  # position=5, collapsed=False

try:
    history.apply([Edit(1, 4, "X"), Edit(3, 5, "Y")])
except ValueError as exc:  # OverlappingEditsError；版本保持不变
    print(exc)
```

## 测试

```sh
python3.11 -m unittest discover -s tests -v
```

测试使用合成数据，覆盖插入吸附、删除/替换折叠与原因、批量重叠原子拒绝、
多版本锚点映射、越界校验、码点语义（emoji、组合字符）以及 CLI 子进程。
