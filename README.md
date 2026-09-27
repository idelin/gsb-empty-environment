# minwitness — 带撤销的最小解释集（最小事实见证集）

布尔结果由 AND/OR 无环电路计算，叶子是可撤销（retract）的事实。
对每个当前为真的根，输出**全局最小**的事实见证集：重复叶子只计一次，
先按事实数量、再按事实 ID 的字典序比较。撤销/断言叶子后，仅对受影响的
祖先子图做**增量**重算（值 + 见证前沿）。

最小见证通过每个节点的 Pareto 前沿（集合包含关系下的反链）全局求得：
AND 取子前沿两两并集、OR 合并子前沿，再剪除被支配集合。因此不会被
"逐子树局部最优拼接" 替代——验收边界 `root=(a OR b) AND (b OR c)` 中，
贪心拼接得到 `{a, b}`（两个事实），而全局最优是 `{b}`。

仅使用 Python 3.11 标准库。所有示例数据均为合成数据。

## 运行

```sh
python3.11 minwitness.py [script文件]   # 省略脚本文件时从标准输入读取
python3.11 minwitness.py examples/boundary.txt
python3.11 -m unittest -v               # 运行测试
```

## 输入格式（命令脚本）

每行一条命令，`#` 之后为注释，空行忽略。节点 ID 为不含空白与 `#` 的字符串。

| 命令 | 含义 |
| --- | --- |
| `leaf ID [asserted\|retracted]` | 声明事实叶子，默认 `retracted` |
| `and ID CHILD...` | 定义 AND 门（子节点须已定义，至少一个） |
| `or ID CHILD...` | 定义 OR 门（同上） |
| `assert ID` | 断言事实叶子 |
| `retract ID` | 撤销事实叶子（增量更新） |
| `root ID` | 指定一个根节点（可多个，按声明顺序输出） |
| `value ID` | 查询节点当前布尔值 |
| `witness [ID]` | 查询最小见证：省略 ID 时对所有根逐个输出 |

## 输出格式

- `value ID` → `ID true` 或 `ID false`
- `witness` → 每个目标一行：
  - 为真：`ID true f1 f2 ...`（最小见证，事实 ID 按字典序、空格分隔）
  - 为假：`ID false`（无见证）

脚本错误（未知节点、重复 ID、未知命令等）输出 `error: line N: ...`
到标准错误并以退出码 2 结束。

## 示例

```
$ python3.11 minwitness.py examples/boundary.txt
root true b
root true a c
root true b
```

## 文件

- `minwitness.py` — 电路引擎 + CLI（`Circuit` 类可直接作为库使用）
- `test_minwitness.py` — unittest 测试（含验收边界与贪心反例对照）
- `examples/boundary.txt` — 验收边界示例脚本（合成数据）
