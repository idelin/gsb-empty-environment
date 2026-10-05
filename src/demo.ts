import { solveOrderSplitting, WarehouseInput, OrderLine } from "./solver";

function show(title: string, warehouses: WarehouseInput[], order: OrderLine[]) {
  console.log(`\n=== ${title} ===`);
  console.log("订单:", order.map((l) => `${l.productId}x${l.quantity}`).join(", ") || "(空)");
  const result = solveOrderSplitting(warehouses, order);
  if (result.status === "infeasible") {
    console.log(`不可行: ${result.reason}（下界 ${result.lowerBound}）`);
    return;
  }
  console.log(`最低总运费: ${result.totalCost}（下界 ${result.lowerBound}，间隙 ${result.totalCost - result.lowerBound}）`);
  console.log(`包裹数: ${result.packages}，超需求件数: ${result.extraUnits}`);
  for (const a of result.allocations) {
    console.log(`  仓 ${a.warehouseId} -> ${a.productId} x ${a.quantity}`);
  }
}

// 场景 1：免运门槛——多取一件反而更便宜
show(
  "凑免运门槛",
  [
    { id: "A", baseFee: 10, perItemFee: 2, freeShippingThreshold: 5, stock: { 键盘: 10 } },
    { id: "B", baseFee: 3, perItemFee: 1, stock: { 键盘: 10 } },
  ],
  [{ productId: "键盘", quantity: 4 }],
);

// 场景 2：多商品 + 库存约束拆单
show(
  "多商品拆单",
  [
    { id: "A", baseFee: 8, perItemFee: 1, stock: { 键盘: 2, 鼠标: 3 } },
    { id: "B", baseFee: 5, perItemFee: 2, stock: { 键盘: 5, 鼠标: 1 } },
    { id: "C", baseFee: 2, perItemFee: 3, stock: { 鼠标: 5 } },
  ],
  [
    { productId: "键盘", quantity: 4 },
    { productId: "鼠标", quantity: 4 },
  ],
);

// 场景 3：库存不足
show(
  "不可行",
  [{ id: "A", baseFee: 1, perItemFee: 1, stock: { 键盘: 1 } }],
  [{ productId: "键盘", quantity: 3 }],
);
