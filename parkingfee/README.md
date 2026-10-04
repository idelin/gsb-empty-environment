# parkingfee — 缺失事件的停车计费恢复工具

处理缺失一次进/出事件的停车场闸机记录：在**最多补一个事件**的条件下恢复合法停车段，
按给定计费规则输出**最低与最高可能费用**及对应的**补事件时间区间**。
当费用无法唯一确定时（`status` 为 `ambiguous` / `unbounded`），输出的是费用区间与补事件区间，
绝不猜测成单个账单。

## 计费规则（均作为输入）

- `free_minutes`：每段停车的免费时长；
- `unit_minutes` + `unit_price_cents`：每段停车超出免费时长后，按计费单位**向上进位**计费（每段独立进位）；
- `daily_cap_cents`：每个自然日的费用封顶（按**进场时间**所在自然日归集，时区取时间戳自带偏移）；`0` 表示不封顶。

## 恢复模型

合法记录是 `enter`/`exit` 交替、以 `enter` 开始、以 `exit` 结束的偶数长度序列。

- 记录已合法（偶数长度且无缺陷）：费用唯一，无需补事件；
- 奇数长度且恰有一处可修复点：补事件位置唯一（两个相同类型事件之间，或首/尾），
  但补事件的**时间戳**在相邻观测事件之间连续可变，费用是该时间戳的分段常值函数，
  工具对其做完整扫描，给出最低/最高费用及各自对应的补事件区间；
- 其余情况（偶数长度有缺陷、多处缺陷、端点无法修复）：`unrecoverable`；
- 缺少出场事件且不设日封顶时费用无上界：`unbounded`，最高费用输出为 `null`。

补事件区间端点为计费单位边界，端点本身是否取到不影响费用量级。

## 使用

```sh
go build -o parkingfee .
./parkingfee -in example.json     # 或从 stdin 读入
```

输入（JSON）：

```json
{
  "events": [{"time": "2026-01-05T08:00:00+08:00", "type": "enter"}, ...],
  "free_minutes": 15,
  "unit_minutes": 30,
  "unit_price_cents": 100,
  "daily_cap_cents": 1000
}
```

输出（JSON）要点：

- `status`：`ok`（费用唯一）/ `ambiguous`（费用有范围）/ `unbounded`（无上界）/ `unrecoverable` / `invalid`；
- `inserted_event`：补事件在完整序列中的位置、类型及相邻观测事件时间；
- `min` / `max`：`fee_cents`、取到该极值的补事件区间 `insert_windows`（`null` 端点表示该侧开放），
  以及一组实现该费用的代表性停车段 `segments`（段费用为封顶前金额，总额按日封顶）。

## 测试

```sh
go test ./...
```
