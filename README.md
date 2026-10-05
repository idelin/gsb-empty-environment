# 批次库存账本(batch-inventory-ledger)

按到期日出库(FEFO)的批次库存实现,事件溯源,支持历史时点库存与当前分配。

## 核心语义

- **FEFO 分配**:候选批次按 `(expiryDate, inboundSeq)` 升序 —— 先按到期日,同到期日按入库序号决胜。
- **过期隔离**:`expiryDate <= at`(按日期部分比较)的批次不参与预留/出库分配,在视图中计入 `expiredOnHand`。
- **预留可取消**:`reserve` 冻结数量,`cancelReservation` 释放剩余未出库部分;`ship` 支持部分出库。
- **迟到入库不改写历史**:所有分配结果记录在事件上;`inbound` 的 `at` 可以早于已提交的出库,历史回放只机械应用已记录的分配,已完成出库的批次归属不变。
- **历史时点库存**:`inventoryAt(t)` 按 `(at, seq)` 重放 `at <= t` 的事件重建状态;`inventoryNow()` 给出当前视图,`activeReservations()` 给出当前分配。
- **退货回原批次**:`returnGoods(shipmentId, qty, at)` 沿出库时记录的分配顺序退回原批次,超额退货抛 `OverReturnError`。
- **守恒不变量**:每批次 `received + returned === onHand + shipped` 且 `0 <= reserved <= onHand`;`assertConserved()` / `verifyView()` 校验,同刻竞争预留按提交顺序结算、总量不超分。

## API 摘要

```ts
const led = new InventoryLedger();
led.inbound({ batchId: "A", qty: 10, expiryDate: "2027-01-01", at: "2026-01-01T00:00:00Z" });
led.reserve("O1", 6, at);                 // -> Allocation[](FEFO)
led.cancelReservation("O1", at);          // 释放剩余
led.ship("O1", at, qty?);                 // -> { shipmentId, allocations },归属冻结
led.returnGoods(shipmentId, qty, at);     // 退回原批次
led.inventoryAt(t);                       // 历史时点视图
led.inventoryNow();                       // 当前视图
led.activeReservations();                 // 当前分配
led.assertConserved();                    // 守恒校验
```

时间戳使用 ISO 8601 字符串,按字典序比较;过期判定只比较日期部分(`YYYY-MM-DD`)。

## 运行测试

```sh
bun test
```

覆盖:FEFO 排序与入库序号决胜、预留取消、退货回原批次(跨批次出库单)、过期隔离、迟到入库不改写已完成出库归属、同刻预留竞争守恒、历史时点重放、混合场景逐步守恒。
