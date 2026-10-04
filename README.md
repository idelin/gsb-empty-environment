# refund — 分批退款计算库

订单包含商品行、运费行与已分摊优惠，支持分批退款，并保证：

- **累计精确**：整单退完时，累计退款额 == 整单可退额（实付 - 已分摊优惠），分文不差。
- **尾差确定**：商品行累计退款额 = `floor(可退额 × 累计已退件数 / 总件数)`，
  除不尽的尾差确定性地落在最后几件上。
- **顺序无关**：累计退款额只取决于累计已退件数/金额，与批次划分、退款顺序无关，
  任何退款路径下每件商品的最终可退额一致。
- **幂等**：`RequestID` 相同的重复请求返回首次结果（`Duplicate=true`），不重复计费；
  同一 `RequestID` 携带不同内容返回 `ErrRequestConflict`。
- **可审计**：每行结果带 `Basis` 字段，逐分钱说明计算依据。

## 用法

```go
c, _ := refund.NewCalculator(refund.Order{
    ID: "order-1",
    Lines: []refund.Line{
        {ID: "item-a", Kind: refund.LineKindItem, Qty: 3, Paid: 1000},          // 商品
        {ID: "ship", Kind: refund.LineKindShipping, Paid: 200, Discount: 50},   // 运费
    },
})

res, _ := c.Apply(refund.Request{
    RequestID: "req-1",
    Lines: []refund.LineRefund{
        {LineID: "item-a", Qty: 2},      // 商品按件退
        {LineID: "ship", Amount: 100},   // 运费按金额退，超出可退额自动截断
    },
})
// res.Lines[i].Basis => "floor(1000*2/3) - floor(1000*0/3) = 666 - 0 = 666"
```

## 测试

```sh
GOCACHE=/private/tmp/gocache GO111MODULE=on go test ./...
```
