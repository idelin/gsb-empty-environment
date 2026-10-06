# alertjoin

两路告警按键（id）连接的 Go 连接器：两侧事件时间差绝对值不超过 `d` 时输出一次匹配对。

## 语义

- **按键匹配**：左右两侧各维护事件缓冲，同 id 且 `|tLeft - tRight| <= d` 时输出一次 `Pair`。
- **重复与冲突**：同 id 完全相同的事件重复投递会被忽略；同 id 但内容（含时间戳）不同则返回 `*ConflictError`。
- **水位回收**：
  - 匹配缓冲中的事件，只有**对方**水位越过 `event.Time + d` 才会回收（此后对方不可能再送来能匹配的事件）。
  - 去重/冲突墓碑保留到**本侧**水位越过 `event.Time`（此前重复投递仍可能合法到达）。
- **前提**：每侧事件时间不得早于本侧当前水位；两侧水位可各自独立（非同步）前进，输入允许乱序。

## 用法

```go
j := alertjoin.NewJoiner(5) // 窗口 d = 5
_ = j.AddLeft(alertjoin.Event{ID: "a", Time: 100, Content: "cpu high"})
_ = j.AddRight(alertjoin.Event{ID: "a", Time: 103, Content: "cpu high"})
j.Pairs() // => [{Left:{a 100 ...} Right:{a 103 ...}}]

j.AdvanceLeftWatermark(200) // 回收右侧 Time+5 < 200 的事件
```

## 测试

```sh
go test ./...                      # 单元测试 + 300 组随机核对
ALERTJOIN_ITERS=20000 go test ./... # 加大随机核对规模
```

随机核对（`TestBruteForceCrossCheck`）对每侧注入重复投递、乱序交织两路输入、
两侧水位非同步推进，最终将连接器输出与全量笛卡尔积筛选（按 id 去重后
`|dt| <= d` 过滤）的结果逐一比对，并校验水位冲刷后缓冲清空。
