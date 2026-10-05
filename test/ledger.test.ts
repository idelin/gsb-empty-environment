import { describe, expect, test } from "bun:test";
import {
  BatchConflictError,
  DuplicateOrderError,
  InsufficientInventoryError,
  InventoryLedger,
  OverReturnError,
  UnknownOrderError,
  verifyView,
} from "../src/ledger";

const T = {
  t0: "2026-01-01T00:00:00Z",
  t1: "2026-01-02T00:00:00Z",
  t2: "2026-01-03T00:00:00Z",
  t3: "2026-01-04T00:00:00Z",
  t4: "2026-01-05T00:00:00Z",
  t5: "2026-01-06T00:00:00Z",
};

describe("FEFO 分配:按到期日,再按入库序号决胜", () => {
  test("不同到期日按到期日升序;同到期日按入库序号升序", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "B1", qty: 5, expiryDate: "2026-12-01", at: T.t0 });
    led.inbound({ batchId: "B2", qty: 5, expiryDate: "2026-11-01", at: T.t1 });
    led.inbound({ batchId: "B3", qty: 5, expiryDate: "2026-12-01", at: T.t2 });

    const alloc = led.reserve("O1", 7, T.t3);
    // B2 最早到期先出;B1 与 B3 同到期日,B1 入库序号小先出。
    expect(alloc).toEqual([
      { batchId: "B2", qty: 5 },
      { batchId: "B1", qty: 2 },
    ]);
    const view = led.assertConserved(T.t4);
    expect(view.totals.reserved).toBe(7);
    expect(view.totals.available).toBe(8);
  });

  test("同一批次重复入库保留原入库序号", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 1, expiryDate: "2026-12-01", at: T.t0 });
    led.inbound({ batchId: "B", qty: 1, expiryDate: "2026-12-01", at: T.t1 });
    led.inbound({ batchId: "A", qty: 1, expiryDate: "2026-12-01", at: T.t2 });
    const alloc = led.reserve("O1", 2, T.t3);
    expect(alloc).toEqual([{ batchId: "A", qty: 2 }]);
  });

  test("同批次不同到期日入库被拒绝", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 1, expiryDate: "2026-12-01", at: T.t0 });
    expect(() =>
      led.inbound({ batchId: "A", qty: 1, expiryDate: "2027-01-01", at: T.t1 }),
    ).toThrow(BatchConflictError);
  });
});

describe("预留与取消", () => {
  test("取消预留释放全部剩余数量回可用", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 10, expiryDate: "2027-01-01", at: T.t0 });
    led.reserve("O1", 6, T.t1);
    const released = led.cancelReservation("O1", T.t2);
    expect(released).toEqual([{ batchId: "A", qty: 6 }]);
    const view = led.assertConserved(T.t3);
    expect(view.totals.available).toBe(10);
    expect(view.totals.reserved).toBe(0);
    expect(led.activeReservations()).toEqual([]);
  });

  test("重复预留与取消未知订单报错", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 10, expiryDate: "2027-01-01", at: T.t0 });
    led.reserve("O1", 1, T.t1);
    expect(() => led.reserve("O1", 1, T.t1)).toThrow(DuplicateOrderError);
    expect(() => led.cancelReservation("NOPE", T.t1)).toThrow(UnknownOrderError);
  });
});

describe("退货回到原批次", () => {
  test("跨批次出库后,退货沿原分配顺序退回原批次", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "B1", qty: 5, expiryDate: "2026-12-01", at: T.t0 });
    led.inbound({ batchId: "B2", qty: 5, expiryDate: "2026-11-01", at: T.t0 });
    led.reserve("O1", 7, T.t1); // B2:5, B1:2
    const { shipmentId, allocations } = led.ship("O1", T.t2);
    expect(allocations).toEqual([
      { batchId: "B2", qty: 5 },
      { batchId: "B1", qty: 2 },
    ]);

    const back1 = led.returnGoods(shipmentId, 4, T.t3);
    expect(back1).toEqual([{ batchId: "B2", qty: 4 }]);
    let view = led.assertConserved(T.t3);
    const b2 = view.batches.find((b) => b.batchId === "B2")!;
    const b1 = view.batches.find((b) => b.batchId === "B1")!;
    expect(b2.onHand).toBe(4);
    expect(b1.onHand).toBe(3);

    const back2 = led.returnGoods(shipmentId, 3, T.t4);
    expect(back2).toEqual([
      { batchId: "B2", qty: 1 },
      { batchId: "B1", qty: 2 },
    ]);
    view = led.assertConserved(T.t4);
    expect(view.totals.onHand).toBe(10);
    expect(view.totals.shipped).toBe(7);
    expect(view.totals.returned).toBe(7);
  });

  test("退货数量超过未退余额被拒绝", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 5, expiryDate: "2027-01-01", at: T.t0 });
    led.reserve("O1", 5, T.t1);
    const { shipmentId } = led.ship("O1", T.t2);
    led.returnGoods(shipmentId, 5, T.t3);
    expect(() => led.returnGoods(shipmentId, 1, T.t4)).toThrow(OverReturnError);
  });
});

describe("过期隔离", () => {
  test("过期批次不参与分配,并在视图中单独计量", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "OLD", qty: 10, expiryDate: "2026-01-01", at: "2025-06-01T00:00:00Z" });
    led.inbound({ batchId: "NEW", qty: 3, expiryDate: "2027-01-01", at: "2025-06-01T00:00:00Z" });

    expect(() => led.reserve("O1", 5, T.t2)).toThrow(InsufficientInventoryError);
    const alloc = led.reserve("O2", 3, T.t2);
    expect(alloc).toEqual([{ batchId: "NEW", qty: 3 }]);

    const view = led.assertConserved(T.t2);
    const old = view.batches.find((b) => b.batchId === "OLD")!;
    expect(old.expired).toBe(true);
    expect(view.totals.expiredOnHand).toBe(10);
    expect(view.totals.available).toBe(0);
    expect(view.totals.reserved).toBe(3);
  });

  test("到期日当天即视为过期", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 5, expiryDate: "2026-01-03", at: T.t0 });
    expect(() => led.reserve("O1", 1, "2026-01-03T08:00:00Z")).toThrow(
      InsufficientInventoryError,
    );
  });
});

describe("迟到入库不改写已完成出库的批次归属", () => {
  test("at 早于出库的迟到入库追加后,历史归属与守恒保持不变", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 10, expiryDate: "2027-06-01", at: T.t1 });
    led.reserve("O1", 6, T.t2); // A:6
    const { shipmentId, allocations } = led.ship("O1", T.t3);
    expect(allocations).toEqual([{ batchId: "A", qty: 6 }]);

    // 迟到入库:生效时间 t2(出库之前),但到期日更早。若重新计算历史会改派给 LATE。
    led.inbound({ batchId: "LATE", qty: 5, expiryDate: "2026-02-01", at: T.t2 });

    // 已完成出库的批次归属不变。
    const back = led.returnGoods(shipmentId, 6, T.t4);
    expect(back).toEqual([{ batchId: "A", qty: 6 }]);

    // 历史时点:t3 之后 LATE 已在账上,但出库归属仍是 A。
    const atT3 = led.inventoryAt(T.t3);
    verifyView(atT3);
    const late = atT3.batches.find((b) => b.batchId === "LATE")!;
    const a = atT3.batches.find((b) => b.batchId === "A")!;
    expect(late.onHand).toBe(5);
    expect(a.shipped).toBe(6);
    expect(a.onHand).toBe(4);

    // 迟到入库生效时点之前的历史不受影响。
    const atT1 = led.inventoryAt(T.t1);
    expect(atT1.batches.map((b) => b.batchId)).toEqual(["A"]);
    expect(atT1.totals.onHand).toBe(10);

    // 当前分配:新预留按 FEFO 走,LATE 到期更早先出。
    const alloc2 = led.reserve("O2", 2, T.t5);
    expect(alloc2).toEqual([{ batchId: "LATE", qty: 2 }]);
    led.assertConserved(T.t5);
  });
});

describe("同刻预留竞争的守恒", () => {
  test("同一 at 的竞争预留按提交顺序结算,总量不超分", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 10, expiryDate: "2027-01-01", at: T.t0 });

    led.reserve("O1", 7, T.t1);
    expect(() => led.reserve("O2", 7, T.t1)).toThrow(InsufficientInventoryError);
    led.reserve("O2", 3, T.t1); // 剩余 3 恰好够

    const view = led.assertConserved(T.t1);
    expect(view.totals.reserved).toBe(10);
    expect(view.totals.available).toBe(0);
    expect(view.totals.onHand).toBe(10);

    // 取消其中一个,数量完整回到可用。
    led.cancelReservation("O1", T.t1);
    const after = led.assertConserved(T.t1);
    expect(after.totals.available).toBe(7);
    expect(after.totals.reserved).toBe(3);

    // 同刻再竞争:超额者失败,守恒保持。
    led.reserve("O3", 7, T.t1);
    expect(() => led.reserve("O4", 1, T.t1)).toThrow(InsufficientInventoryError);
    const final = led.assertConserved(T.t1);
    expect(final.totals.reserved).toBe(10);
    expect(final.totals.available).toBe(0);
  });
});

describe("历史时点库存", () => {
  test("inventoryAt 逐步重放与当前分配一致", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 10, expiryDate: "2027-01-01", at: T.t0 });
    led.inbound({ batchId: "B", qty: 5, expiryDate: "2026-06-01", at: T.t1 });
    led.reserve("O1", 4, T.t2); // B:4
    led.ship("O1", T.t3, 3); // 部分出库 B:3
    led.cancelReservation("O1", T.t4); // 释放剩余 B:1

    const atT0 = led.inventoryAt(T.t0);
    expect(atT0.totals).toMatchObject({ onHand: 10, reserved: 0, shipped: 0 });

    const atT2 = led.inventoryAt(T.t2);
    expect(atT2.totals).toMatchObject({ onHand: 15, reserved: 4, available: 11, shipped: 0 });

    const atT3 = led.inventoryAt(T.t3);
    expect(atT3.totals).toMatchObject({ onHand: 12, reserved: 1, shipped: 3 });

    const atT4 = led.inventoryAt(T.t4);
    expect(atT4.totals).toMatchObject({ onHand: 12, reserved: 0, available: 12, shipped: 3 });

    for (const t of [T.t0, T.t1, T.t2, T.t3, T.t4]) verifyView(led.inventoryAt(t));

    // 部分出库后预留仍活跃,剩余 1 取消前可在当前分配中看到。
    const active = led.activeReservations();
    expect(active).toEqual([]);
  });

  test("部分出库后取消只释放剩余数量", () => {
    const led = new InventoryLedger();
    led.inbound({ batchId: "A", qty: 10, expiryDate: "2027-01-01", at: T.t0 });
    led.reserve("O1", 6, T.t1);
    led.ship("O1", T.t2, 4);
    const released = led.cancelReservation("O1", T.t3);
    expect(released).toEqual([{ batchId: "A", qty: 2 }]);
    const view = led.assertConserved(T.t3);
    expect(view.totals).toMatchObject({ onHand: 6, reserved: 0, available: 6, shipped: 4 });
  });
});

describe("混合场景全程守恒", () => {
  test("入库/预留/取消/出库/退货/迟到入库交错,每步守恒", () => {
    const led = new InventoryLedger();
    const check = (t: string) => led.assertConserved(t);

    led.inbound({ batchId: "A", qty: 20, expiryDate: "2027-03-01", at: T.t0 });
    check(T.t0);
    led.inbound({ batchId: "B", qty: 8, expiryDate: "2026-08-01", at: T.t0 });
    check(T.t0);

    led.reserve("O1", 10, T.t1); // B:8, A:2
    led.reserve("O2", 5, T.t1); // A:5
    check(T.t1);

    const s1 = led.ship("O1", T.t2, 9); // B:8, A:1
    check(T.t2);
    led.cancelReservation("O1", T.t2); // 释放 A:1
    check(T.t2);

    led.returnGoods(s1.shipmentId, 4, T.t3); // 退回 B:4
    check(T.t3);

    // 迟到入库:生效在 t1,不影响 s1 的归属。
    led.inbound({ batchId: "C", qty: 6, expiryDate: "2026-05-01", at: T.t1 });
    check(T.t3);
    expect(led.returnGoods(s1.shipmentId, 5, T.t4)).toEqual([
      { batchId: "B", qty: 4 },
      { batchId: "A", qty: 1 },
    ]);
    check(T.t4);

    const s2 = led.ship("O2", T.t4); // A:5
    check(T.t4);
    led.returnGoods(s2.shipmentId, 5, T.t5);
    const view = check(T.t5);

    // 终态:全部回到库存,无在途预留。
    expect(view.totals.reserved).toBe(0);
    expect(view.totals.onHand).toBe(34);
    expect(view.totals.received).toBe(34);
    expect(view.totals.shipped).toBe(14);
    expect(view.totals.returned).toBe(14);

    // 每个历史时点同样守恒。
    for (const t of [T.t0, T.t1, T.t2, T.t3, T.t4, T.t5]) {
      verifyView(led.inventoryAt(t));
    }
  });
});
