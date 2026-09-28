# 有界重放去重收件箱（Bounded Replay-Dedup Inbox）

消息按 `(sender, 严格递增序号 seq)` 标识，可乱序到达，但乱序窗口为有限值
`W`。业务状态（`balance`）、每发送者的连续提交游标（`cursor`）、窗口内乱序
缓存（`pending`）三者在**同一个 SQLite 事务**中持久化，因此进程在任意时刻
崩溃后恢复，业务效果恰好一次（exactly-once）。

## 语义

- `seq <= cursor`：幂等忽略（`IGNORED`），不重复加值。
- `seq > cursor + W`：显式拒绝（`REJECTED`），消息不入缓存、游标不推进。
- 其余消息进入窗口缓存（`CACHED`）；每次提交后尝试从 `cursor+1` 开始连续
  排水，排水出的消息在同一事务内加值并推进游标（`APPLIED`）。
- 故障注入：`crash before` 在提交前崩溃（事务整体回滚，恢复后无任何效果，
  重试正常应用一次）；`crash after` 在提交后、返回前崩溃（恢复后效果已在，
  重试被幂等忽略，绝不重复加值）。

## 运行

```bash
python3 inbox.py --db inbox.db --window 3 < commands.txt
python3 -m unittest test_inbox -v   # 全部测试
```

## 输入格式（stdin，每行一条命令，`#` 开头为注释）

| 命令 | 说明 |
| --- | --- |
| `recv <sender> <seq> <value>` | 投递消息，`value` 为整数业务加值 |
| `state` | 打印各发送者游标与业务余额 |
| `crash before\|after` | 武装下一次 `recv` 的崩溃点（仅一次） |
| `help` | 打印帮助 |

## 输出格式（stdout，每个 `recv` 一行）

```
APPLIED  sender=<s> seq=<n> applied=<a,b,...> cursor=<c> balance=<b>
CACHED   sender=<s> seq=<n> cursor=<c> balance=<b>
IGNORED  sender=<s> seq=<n> cursor=<c> balance=<b>
REJECTED sender=<s> seq=<n> cursor=<c> balance=<b>
STATE    cursor={<sender>: <c>, ...} balance=<b>
```

崩溃时向 stderr 打印 `CRASH before-commit` 或 `CRASH after-commit`，
进程退出码 3；用同一 DB 文件重新运行即恢复。

## 验收示例（W=3，已提交 0）

输入：

```
recv alice 3 30
recv alice 4 40
recv alice 1 10
recv alice 2 20
recv alice 1 10
state
```

输出：

```
CACHED   sender=alice seq=3 cursor=0 balance=0
REJECTED sender=alice seq=4 cursor=0 balance=0
APPLIED  sender=alice seq=1 applied=1       cursor=1 balance=10
APPLIED  sender=alice seq=2 applied=2,3     cursor=3 balance=60
IGNORED  sender=alice seq=1 cursor=3 balance=60
STATE    cursor={'alice': 3} balance=60
```

全部数据均为合成数据；业务效果为演示用的整数累加。
