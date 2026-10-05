# refund — 分批退款计算库

Go 实现的分批退款计算器，面向“订单含商品、运费与已分摊优惠，需多批次退款”的场景。

## 核心保证

- **累计退款 = 整单可退额**：整单可退额在 `NewSession` 时固定为
  `Σ(单价×数量 − 行已分摊优惠) + (运费 − 运费优惠)`，全部退完后累计退款恒等于该值。
- **确定性尾差分配**：行可退额 ÷ 件数除不尽时，余数 R 个“分”固定补给
  单位序号 `0..R-1` 的商品。尾差只依赖单位序号，不依赖退款发生的先后。
- **顺序无关**：同一行无论分几批、按什么顺序退，每一件商品的最终可退额
  完全相同（见 `TestOrderIndependence`）。
- **幂等**：每个请求必须携带幂等键 `Key`；重复键直接返回首次结果
  （`Duplicate=true`），不重复计费。被拒绝的请求不占用键。
- **逐分可审计**：每笔分摊输出 `Components`（商品价 / 优惠扣回 / 尾差 /
  运费），每个成分带人类可读的 `Reason`，成分之和恒等于分摊额。

## 用法

```go
s, _ := refund.NewSession(refund.Order{
    Lines: []refund.Line{
        {ID: "A", UnitPriceCents: 1000, Qty: 3, DiscountCents: 100}, // 可退 2900
    },
    ShippingCents:         500,
    ShippingDiscountCents: 50, // 运费可退 450
})

res, _ := s.Refund(refund.Request{
    Key:   "batch-1",               // 幂等键
    Items: []refund.ItemRefund{{LineID: "A", Qty: 1}},
})
// res.Allocations[0].Components → 每件 967 分的构成：
//   unit_price +1000 / discount_clawback -34 / remainder +1
```

金额一律使用**分**（`int64`），杜绝浮点误差。

## 运行测试

```sh
go test ./...
```
