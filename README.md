# 变更驱动的两表左外连接视图

Python 3.11 + 标准库（`dataclasses`、`collections`、`json`、`unittest`）实现的增量
物化视图：消费本地夹具中的行级变更，维护

```
left LEFT OUTER JOIN right ON left.k = right.k
```

并在每个事务提交时发布该事务的**净值**变更日志。不依赖、也不连接任何消息平台。

## 语义

- 左行到来且右侧无匹配：发布 1 条右列补 `NULL` 的扩展行。
- **首个**匹配右行到来：撤销（-1）NULL 扩展行，发布连接行。
- 后续匹配右行到来：每对发布 1 条连接行（1 左 2 右 => 2 行，重数正确）。
- 右行删除但仍有其它匹配（如 2 条删 1 条）：仅撤销对应连接行，**不补** NULL。
- **最后一条**匹配右行删除：撤销连接行并恢复（+1）NULL 扩展行。
- 左右两侧在**同一事务**中变化时，先在视图内部重放、按行求和抵消，
  只发布最终一致结果；事务内"插入又删除"等净值为零的抖动不发布。
- 按 `commit_seq` 顺序提交（同事务必须共用一个序号，序号全局唯一）。

输出行左列加 `l_` 前缀、右列加 `r_` 前缀，无匹配时 `r_*` 为 `None`。

## 变更记录（本地夹具）

`fixtures/changes.json` 中的每条记录包含：事务 ID（`txn_id`）、表（`left`/`right`）、
主键（`pk`）、前值 `before`、后值 `after`、提交序号 `commit_seq`：

| 事务 | 内容 | 提交发布 |
| --- | --- | --- |
| T1 | 左 A 与右 A/100 同事务插入 | + (A, 100)，无 NULL 中间行 |
| T2 | 右 A/200 插入 | + (A, 200) => A 重数 2 |
| T3 | 删除右 A/100（仍剩 A/200） | - (A, 100)，不补 NULL |
| T4 | 左 B 与右 B/300 同事务插入 | + (B, 300) |
| T5 | 左 C 插入，右 C/400 同事务插入后删除 | 右行抖动净值为零；+ (C, NULL) |
| T6 | 删除最后一条右 A/200 | - (A, 200)，+ (A, NULL) |

## 运行

```
python3 demo.py
```

输出：

```
Loaded 10 change record(s) from fixtures/changes.json
commit 1 (T1): 0 retraction(s), 1 addition(s)
    + {l_k='A', l_lv=1, r_rv=100}
commit 2 (T2): 0 retraction(s), 1 addition(s)
    + {l_k='A', l_lv=1, r_rv=200}
commit 3 (T3): 1 retraction(s), 0 addition(s)
    - {l_k='A', l_lv=1, r_rv=100}
commit 4 (T4): 0 retraction(s), 1 addition(s)
    + {l_k='B', l_lv=2, r_rv=300}
commit 5 (T5): 0 retraction(s), 1 addition(s)
    + {l_k='C', l_lv=3, r_rv=None}
commit 6 (T6): 1 retraction(s), 1 addition(s)
    - {l_k='A', l_lv=1, r_rv=200}
    + {l_k='A', l_lv=1, r_rv=None}

Final materialised LEFT OUTER JOIN view:
  {l_k='A', l_lv=1, r_rv=None}
  {l_k='B', l_lv=2, r_rv=300}
  {l_k='C', l_lv=3, r_rv=None}
```

## 测试

```
python3 -m unittest discover -s tests -v
```

13 个用例覆盖：NULL 扩展、首个匹配撤销 NULL、重数（1 左 2 右 = 2）、删 1 条右行
不补 NULL、最后匹配删除恢复 NULL、右行就地更新的中间 NULL 抵消、右连接键迁移、
左行删除级联、同事务左右变化只发最终结果、事务内插入即删除的净值抑制、提交序号
乱序重放、脏事务拒绝、夹具端到端结果。

最近一次结果：

```
Ran 13 tests in 0.001s

OK
```

## 代码结构

- `joinview/model.py` — `Change`：事务 ID / 表 / 主键 / 前后值 / 提交序号。
- `joinview/engine.py` — `LeftOuterJoinView`：四组增删原语，返回带符号增量。
- `joinview/runner.py` — `JoinChangelog`：按事务分组、按 `commit_seq` 提交、
  净值抵消后发布；`freeze()` 把输出行变为可计数的确定性键。
- `joinview/fixture.py` — 从本地 JSON 读取夹具。
- `demo.py` — 运行夹具并打印每个提交与最终物化视图。
- `tests/test_joinview.py` — unittest 用例。
