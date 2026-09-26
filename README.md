# 离线同步重命名仲裁（sync.py）

纯 Python 3.11 标准库实现。对带稳定对象 ID 的目录树做离线同步：
事件为 create / rename / move / delete，带因果依赖（deps）与全序决胜戳
`(lamport_clock, replica_id)`。任意符合因果的投递顺序收敛到同一状态，
同名冲突的败者进入明确的冲突目录 `/.conflicts/` 并输出冲突原因，
目录成环的移动被拒绝，删除墓碑不被旧事件复活。

## 运行

```bash
python3 sync.py apply    examples/name-conflict.json          # 按文件顺序投递
python3 sync.py apply    examples/name-conflict.json --json   # JSON 输出
python3 sync.py converge examples/name-conflict.json          # 枚举所有因果顺序验证收敛
python3 -m unittest -v                                          # 运行测试
```

## 输入格式

JSON 文件，顶层为 `{"events": [...]}`（或直接是事件数组）。每个事件：

| 字段      | 类型        | 必填 | 说明                                       |
|-----------|-------------|------|--------------------------------------------|
| `id`      | string      | 是   | 事件唯一 ID                                |
| `op`      | string      | 是   | `create` / `rename` / `move` / `delete`    |
| `obj`     | string      | 是   | 稳定对象 ID                                |
| `clock`   | int         | 是   | Lamport 时钟                               |
| `replica` | string      | 是   | 副本 ID；与 clock 组成全序决胜戳           |
| `deps`    | [string]    | 否   | 因果依赖：必须先投递的事件 ID              |
| `name`    | string      | 条件 | create/rename 的目标名字                   |
| `parent`  | string      | 条件 | create/move 的目标父对象 ID，`"root"` 为根 |

决胜戳 `(clock, replica)` 字典序全序，须全局唯一。`apply` 按文件顺序投递，
若某事件的 `deps` 尚未投递则报错退出（退出码 2）。

## 输出格式

`apply` 输出五个小节（`--json` 时为对应字段）：

- `tree`：最终路径 → 对象 ID。
- `conflicts`：同名冲突记录，含 `obj`（败者）、`winner`、`reason`（冲突原因）。
- `rejected`：被拒绝的事件（如会成环的移动），含原因。
- `orphans`：父目录缺失/已删而挂回根目录的对象。
- `tombstones`：删除墓碑（对象 → 删除戳）。

`converge` 额外输出 `status: converged | truncated | diverged` 与
`orders_tested`（实际枚举的因果投递顺序数）。

## 仲裁规则（确定性，因此与因果投递顺序无关）

1. **名字/父目录**：每对象取决胜戳最大的 create/rename（名字）与
   create/move（父目录）事件，即 last-writer-wins。
2. **删除墓碑**：delete 压制所有比它旧的事件，旧事件不能复活对象；
   比删除更新（因果上更晚）的事件仍生效。
3. **禁环**：父指针成环时，拒绝环上决胜戳最大的父指针事件
   （即最后造成环的那个移动），该对象回退到次新的父指针事件，
   重复至无环。拒绝被记录于 `rejected`。
4. **同名冲突**：同目录同名的存活对象中，名字事件决胜戳最大者保留原名，
   败者移入 `/.conflicts/`；冲突目录内仍撞名时追加 `#对象ID` 消歧。
5. **孤儿**：父对象不存在或已删除的对象挂回根目录并记录。

收敛性说明：物化函数 `SyncNode.materialize()` 是已投递事件**集合**的纯函数，
不依赖投递顺序；`converge` 命令与测试通过枚举/随机采样因果顺序实际验证。

## 已知限制

- 仲裁规则是确定性的 LWW 家族策略，未证明在任何意义下"最优"
  （例如被成环拒绝的移动不做跨对象重定位补偿）。
- `converge` 的枚举随事件数阶乘增长，默认上限 20000 种顺序，
  超出时报告 `truncated` 而非 `converged`。
- 所有示例与测试数据均为合成数据。
