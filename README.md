# 订单被插到了撮合队列前面

撮合引擎按价格时间优先，攻击者伪造时间戳或重发撤单再下单，把自己的订单插到同价队列前。请实现供Go撮合引擎调用的可导入订单排序组件。订单序号由引擎单调分配，时间戳与客户端声明不参与排序；撤单重发获得新序号。自成交、洗售与异常频率检测。用序号伪造、撤单插队、自成交、合法订单和频率验证排序不可操纵。

## 组件

包 `netsec`（module `example.org/netsec/q0456`）提供两个可直接导入的类型。

- `OrderBook`：并发安全的价格-时间优先订单簿。排序只依据引擎控制的数据：
  - 价格优先：买单高价优先、卖单低价优先；
  - 同价 FIFO：以 `Submit` 成功时引擎分配的单调递增 `Sequence` 为准。
- `Monitor`：自成交拦截（STP）、洗售上报、基于引擎时钟滑动窗口的报单/撤单
  异常频率检测；支持 `LinkAccounts` 把多个账户归为同一实际控制人。

### 不可操纵性保证

- `OrderRequest.ClientSeq` 与 `OrderRequest.ClientTime` 仅留存审计，任何排序路径
  都不读取；序号字段只存在于引擎构造的 `Order` 上，客户端无法提交。
- 撤单立即从价格层级链表摘除；重发是一次新的 `Submit`，领取下一个序号，只能排到
  同价队列尾部，无法“插队”。
- 被拒绝的请求（校验失败、频率超限、自成交）不消耗序号、不进入队列。
- 频率窗口的时间来自注入的 `Clock`（默认 `time.Now`），客户端声明的时间无法影响。

## 用法

```go
package main

import (
	"fmt"
	"time"

	"example.org/netsec/q0456"
)

func main() {
	monitor := netsec.NewMonitor(netsec.WithFrequencyRule(netsec.FrequencyRule{
		Window:         time.Second,
		MaxSubmissions: 100,
		MaxCancels:     50,
	}))
	monitor.LinkAccounts("trader-a", "trader-a-hedge")

	book := netsec.NewOrderBook("BTC", netsec.WithMonitor(monitor))

	order, err := book.Submit(netsec.OrderRequest{
		Symbol:  "BTC",
		Owner:   "trader-b",
		OrderID: "cli-0001",
		Side:    netsec.SideBuy,
		Price:   100,
		Qty:     2,
		// 以下字段仅审计，不参与排序
		ClientSeq:  999,
		ClientTime: time.Now().Add(-time.Hour),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("engine sequence:", order.Sequence)

	// 撤单后重发：获得新序号，排到同价队列尾部
	if _, err := book.Cancel("cli-0001"); err != nil {
		panic(err)
	}

	// 成交后调用监控记录；同一实际控制人成交将产生洗售告警
	if v := monitor.RecordTrade("trader-a", "t1", "trader-a-hedge", "m1", time.Now()); v != nil {
		fmt.Println("wash trade:", v.Kind)
	}

	for _, v := range monitor.Violations() {
		fmt.Printf("violation: %s owner=%s at=%s\n", v.Kind, v.Owner, v.At)
	}
}
```

## 主要 API

- 排序：`NewOrderBook`、`Submit`、`Cancel`、`Pop`、`BestPrice`、`Queue`、`Snapshot`、`NextSequence`
- 监控：`NewMonitor`、`WithFrequencyRule`、`LinkAccounts`、`SameOwner`、`RecordTrade`、`Violations`
- 哨兵错误：`ErrSelfTrade`、`ErrRateLimited`、`ErrOrderNotFound`、`ErrDuplicateOrderID` 及参数校验错误

## 测试

```sh
go test ./... -race -count=1
```

测试覆盖：序号伪造（客户端序号/时间戳任意取值不改变顺序）、撤单重发只能入队尾、
价格时间优先（买卖双侧）、自成交拦截与关联账户、洗售检测、滑动窗口频率（窗口过期
恢复）、并发下序号唯一单调、非法参数拒绝。
