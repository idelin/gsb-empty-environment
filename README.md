# recur — finite weekly recurrence with revisions and exceptions

Go 标准库实现的周例会改期引擎：日历计算库（`recur` 包）+ JSON 命令行
（`cmd/recurctl`）。只用固定 UTC 偏移与 `time` 包的日期计算，不加载地区
时区数据库。

## 模型

- 规则从给定周一日期开始，每周一个实例，共 `weeks` 个有限周期。
- 实例身份固定为 `规则ID#周序号`；修改星期、分钟、持续时间或固定 UTC
  偏移都不改变身份。周序号永远按原规则起点（`StartDate`）计算
  （`Rule.SeqForDate`），改偏移不会导致 ID 漂移。
- 范围修订（`Revision`）从 `from_seq` 起生效；同 `from_seq` 以 `rev` 大
  者为准。
- 单次例外（`Exception`）：`override` 优先于范围规则；同序号取 `rev`
  大者，同 `rev` 时 `cancel` > `override` > `restore`；`restore` 撤掉
  取消标记，回到范围规则。
- 查询用 UTC 半开区间 `[from,to)` 与实际持续区间 `[start,end)` 相交判断。

## 索引

`Index` 把实例物化在按开始时间排序的切片里：查询二分定位候选窗口
（回看宽度受最大持续时间约束），不展开历史周；更新只重算受影响的周
（范围修订为 `from_seq` 起，单次例外为单周）并归并回有序切片。
`Rule.QueryReference` 是逐周遍历的参考实现，测试中与索引结果逐一核对。

## JSON 命令行

```sh
go build -o recurctl ./cmd/recurctl

# 查询（时间为 RFC 3339，必须含显式偏移）
./recurctl query -rule examples/standup-v2.json \
  -from 2026-01-01T00:00:00Z -to 2026-02-01T00:00:00Z

# 展开全部实例
./recurctl expand -rule examples/standup-v2.json

# 两个规则版本差分：新增 / 移除 / 时间变化
./recurctl diff -old examples/standup-v1.json -new examples/standup-v2.json
```

规则 JSON 见 `examples/`。时间字段输出为 RFC 3339（UTC，`Z` 后缀）加
unix 秒；分钟到秒的换算带溢出检查（`minutesToSeconds`/`addSeconds`）。

## 测试

```sh
go test ./...
```

覆盖：周一改周二只在周二命中、跨午夜、偏移改变后身份不漂移、替代到
查询边界外、取消再恢复、同修订号取消优先、有限周期截断、参数与溢出
校验、索引与逐周参考实现的随机对照、版本差分。
