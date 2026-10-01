# slidewin — 滑动时间窗精确分位数维护器

基于**序统计跳表**（order-statistics skip list，每层带 span，可 O(log n) 按秩定位）
+ **过期最小堆** 的滑动窗口精确分位数计算器。纯 Go 标准库，无第三方依赖。

## 特性

- 窗口大小可配（验收场景为 10s），窗口区间为半开 `(now-size, now]`，
  事件年龄达到 `size` 即过期
- 乱序插入：时间戳乱序到达的事件正确入窗；已过期的事件直接丢弃（born-expired）
- 精确分位数：严格按 `floor(q*(n-1))` 定位第 k 小元素，全程无批量全量排序
- 单条事件撤回：`Retract(id)` 精确移除一个事件；相等值按 `(value, id)` 去重，
  重复值逐条撤销
- 所有操作 O(log n)

## API

```go
w := slidewin.NewWindow(10 * time.Second)
id, live := w.Add(ts, value)   // 插入事件；已过期则 live=false
w.Advance(ts)                  // 推进窗口头部，触发过期
ok := w.Retract(id)            // 撤回单条事件
v, ok := w.Quantile(0.5)       // 精确分位数，rank = floor(q*(n-1))
n := w.Len()                   // 窗内事件数
```

## 运行

```sh
go test -v ./...    # 测试（含验收场景与随机对拍）
go run ./cmd/demo   # 运行样例
```

## 验收场景

- 窗内含 `[1, 1, 9, 20]` 时 `q=0.5` → `floor(0.5*3)=1` → **1**
- 撤回一条 `1` 后 `[1, 9, 20]`，`q=0.5` → `floor(0.5*2)=1` → **9**
- 相等事件按 `(value, id)` 区分，逐条撤回、互不干扰

## 实现要点

- `window.go`：跳表节点按 `(value, id)` 排序，level 上维护 span（跨越节点数），
  支持 insert/delete/atRank 全 O(log n)；过期用最小堆 + `live` 映射懒删除
