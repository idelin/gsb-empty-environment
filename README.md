# 可更正数据流的均值变化点索引

在一条**可更正**的事件流上增量维护均值变化点：支持新增、更正（同 key
覆盖）、撤回，并向消费方明确发出 `add` / `retract` 通知。仅使用 Python
标准库。

## 语义约定

- 事件以 `(source, seq)` 唯一标识并按键排序；重复提交同一 key 不增加
  样本量（同值为空操作，异值视为更正）。
- 候选点是排序后相邻两个事件之间的间隙 `j | j+1`。
- 左窗口取间隙左侧最近 `left_min`（常量 L）个样本，右窗口取右侧最近
  `right_min`（常量 R）个样本；两侧样本数都必须达到下限才可评估。
- 成立条件为 `|左均值 - 右均值| >= 阈值`，**等于阈值也算成立**。
- 所有均值与比较都用 `fractions.Fraction`，不产生浮点误差；阈值与样本
  值可传 `int`、`Fraction` 或可被 `Fraction(...)` 解析的字符串。
- 结果携带左、右证据集合（实际进入两个窗口的 key）。

## 局部重算

间隙 `j` 的左窗口覆盖位置 `[j-L+1, j]`、右窗口覆盖 `[j+1, j+R]`，所以
位置 `pos` 上的一次增/改/删只可能影响间隙

```
j ∈ [pos - R, pos + L - 1]
```

插入与删除会破坏一个旧相邻对，因此除“变动后”的上述区间外，再对“变动前”
同区间的旧 id 补一次扫描。单次操作触及的候选点数量被 `O(L+R)` 界住，与
样本总量 n 无关。候选点 id 用其左右相邻 key 对表示；若该相邻对已被
插入/删除破坏，则旧结果一定走 `retract`。

需要说明：本实现用排序数组保存事件，列表插入/删除本身是 `O(n)` 的簿记
成本；**候选点评估数量**这一核心资源是 `O(L+R)`。若需要整体
`O(log n + L+R)`，可把存储替换为平衡树而不改变重算逻辑。

## 用法

```python
from fractions import Fraction
from changepoint import ChangePointIndex

idx = ChangePointIndex(left_min=1, right_min=5, threshold=3)
events = []
for seq, value in enumerate([3] * 6 + [0] * 6):
    events += idx.upsert("a", seq, value)   # 新增
idx.upsert("a", 3, Fraction(5))             # 更正历史记录
idx.withdraw("a", 7)                        # 撤回一条记录
events += idx.apply([("upsert", "b", 1, 0), # 批量输入
                     ("withdraw", "a", 2)])

for ev in events:
    if ev.kind == "add":      ...          # 消费方记录新变化点
    elif ev.kind == "retract": ...          # 消费方必须显式撤掉旧结果

for point in idx.change_points():
    point.gap, point.diff, point.left_evidence, point.right_evidence
```

变化点默认按 `(seq, source)` 升序返回；通知也可用 `drain_notifications()`
集中取出。

## 文件与测试

- `changepoint.py`：实现。
- `test_changepoint.py`：以**独立全量重算**为 oracle，逐操作核对索引状态，
  并用消费方模型校验每条 `add` 都对应有效结果、每条 `retract` 都能撤掉
  消费方持有的旧结果；另含 400 步随机增/改/删属性测试与触及数量上界测试。
- `resource_demo.py`：对比全量重算候选点数（随 n 线性增长）与增量单次
  触及候选点数（恒定），直接打印资源增长数据。

```sh
python3 -m unittest test_changepoint -v
python3 resource_demo.py
```

## 覆盖的关键场景

- `L=1, R=5, 阈值=3` 能找到分界；
- 均值差**恰等于**阈值时成立，阈值再大无穷小则不成立；
- 样本数刚够两侧窗口成立、差一个样本时被撤回，补回后重新 `add`；
- 在候选点附近撤回样本：旧结果明确 `retract`，边界移动则以新 id `add`；
- 同 `(source, seq)` 重复不增加样本量；更正只触及局部候选区域；
- 分批（含不同 source、分数序号、更正、撤回混合批次）输入与 oracle 一致。
