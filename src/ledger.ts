/**
 * 批次库存账本(FEFO:按到期日出库,入库序号决胜)。
 *
 * 核心语义:
 * - 事件溯源:所有操作追加为带 (at, seq) 的事件;分配结果直接记录在事件上。
 *   因此迟到入库(at 早于已提交出库)不会改变已完成出库的批次归属。
 * - FEFO 分配:候选批次按 (expiryDate, inboundSeq) 升序;过期批次(expiryDate <= at)隔离。
 * - 历史时点库存:inventoryAt(t) 按 (at, seq) 重放事件,机械应用已记录的分配。
 * - 守恒:对每批次 received + returned === onHand + shipped,且 0 <= reserved <= onHand。
 */

export interface Allocation {
  batchId: string;
  qty: number;
}

export interface BatchView {
  batchId: string;
  expiryDate: string;
  inboundSeq: number;
  received: number;
  returned: number;
  onHand: number;
  reserved: number;
  available: number;
  shipped: number;
  expired: boolean;
}

export interface InventoryTotals {
  received: number;
  returned: number;
  onHand: number;
  reserved: number;
  available: number;
  shipped: number;
  expiredOnHand: number;
}

export interface InventoryView {
  asOf: string;
  batches: BatchView[];
  totals: InventoryTotals;
}

export interface ReservationView {
  orderId: string;
  status: "active" | "cancelled" | "shipped";
  remaining: Allocation[];
}

export class InsufficientInventoryError extends Error {}
export class UnknownOrderError extends Error {}
export class DuplicateOrderError extends Error {}
export class UnknownShipmentError extends Error {}
export class OverReturnError extends Error {}
export class BatchConflictError extends Error {}
export class ConservationViolationError extends Error {}

type InventoryEvent =
  | { kind: "inbound"; seq: number; at: string; batchId: string; qty: number; expiryDate: string; inboundSeq: number }
  | { kind: "reserve"; seq: number; at: string; orderId: string; allocations: Allocation[] }
  | { kind: "cancel"; seq: number; at: string; orderId: string; allocations: Allocation[] }
  | { kind: "ship"; seq: number; at: string; orderId: string; shipmentId: string; allocations: Allocation[] }
  | { kind: "return"; seq: number; at: string; shipmentId: string; allocations: Allocation[] };

interface BatchState {
  batchId: string;
  expiryDate: string;
  inboundSeq: number;
  received: number;
  returned: number;
  onHand: number;
  reserved: number;
  shipped: number;
}

interface Reservation {
  orderId: string;
  status: "active" | "cancelled" | "shipped";
  remaining: Allocation[];
}

interface Shipment {
  shipmentId: string;
  orderId: string;
  at: string;
  allocations: Allocation[];
  returnedPerAlloc: number[];
}

const datePart = (iso: string): string => iso.slice(0, 10);

export const isExpiredAt = (expiryDate: string, at: string): boolean =>
  datePart(expiryDate) <= datePart(at);

function assertPositiveQty(qty: number, what: string): void {
  if (!Number.isFinite(qty) || qty <= 0) {
    throw new RangeError(`${what} qty must be a positive finite number, got ${qty}`);
  }
}

function sumQty(allocations: readonly Allocation[]): number {
  return allocations.reduce((acc, a) => acc + a.qty, 0);
}

function emptyBatches(): Map<string, BatchState> {
  return new Map();
}

function getOrCreateBatch(
  batches: Map<string, BatchState>,
  batchId: string,
  expiryDate: string,
  inboundSeq: number,
): BatchState {
  let b = batches.get(batchId);
  if (!b) {
    b = { batchId, expiryDate, inboundSeq, received: 0, returned: 0, onHand: 0, reserved: 0, shipped: 0 };
    batches.set(batchId, b);
  }
  return b;
}

function applyEvent(batches: Map<string, BatchState>, ev: InventoryEvent): void {
  switch (ev.kind) {
    case "inbound": {
      const b = getOrCreateBatch(batches, ev.batchId, ev.expiryDate, ev.inboundSeq);
      b.received += ev.qty;
      b.onHand += ev.qty;
      break;
    }
    case "reserve": {
      for (const a of ev.allocations) {
        const b = batches.get(a.batchId);
        if (!b) throw new Error(`corrupt log: reserve unknown batch ${a.batchId}`);
        b.reserved += a.qty;
      }
      break;
    }
    case "cancel": {
      for (const a of ev.allocations) {
        const b = batches.get(a.batchId);
        if (!b) throw new Error(`corrupt log: cancel unknown batch ${a.batchId}`);
        b.reserved -= a.qty;
      }
      break;
    }
    case "ship": {
      for (const a of ev.allocations) {
        const b = batches.get(a.batchId);
        if (!b) throw new Error(`corrupt log: ship unknown batch ${a.batchId}`);
        b.reserved -= a.qty;
        b.onHand -= a.qty;
        b.shipped += a.qty;
      }
      break;
    }
    case "return": {
      for (const a of ev.allocations) {
        const b = batches.get(a.batchId);
        if (!b) throw new Error(`corrupt log: return unknown batch ${a.batchId}`);
        b.onHand += a.qty;
        b.returned += a.qty;
      }
      break;
    }
  }
}

function buildView(batches: Map<string, BatchState>, asOf: string): InventoryView {
  const views: BatchView[] = [...batches.values()]
    .map((b) => ({
      batchId: b.batchId,
      expiryDate: b.expiryDate,
      inboundSeq: b.inboundSeq,
      received: b.received,
      returned: b.returned,
      onHand: b.onHand,
      reserved: b.reserved,
      available: b.onHand - b.reserved,
      shipped: b.shipped,
      expired: isExpiredAt(b.expiryDate, asOf),
    }))
    .sort((x, y) => x.expiryDate.localeCompare(y.expiryDate) || x.inboundSeq - y.inboundSeq);

  const totals: InventoryTotals = {
    received: 0,
    returned: 0,
    onHand: 0,
    reserved: 0,
    available: 0,
    shipped: 0,
    expiredOnHand: 0,
  };
  for (const v of views) {
    totals.received += v.received;
    totals.returned += v.returned;
    totals.onHand += v.onHand;
    totals.shipped += v.shipped;
    if (v.expired) {
      totals.expiredOnHand += v.onHand;
    } else {
      totals.reserved += v.reserved;
      totals.available += v.available;
    }
  }
  return { asOf, batches: views, totals };
}

/** 校验视图守恒:received + returned === onHand + shipped,且预留不越界。 */
export function verifyView(view: InventoryView): void {
  for (const b of view.batches) {
    if (b.received + b.returned !== b.onHand + b.shipped) {
      throw new ConservationViolationError(
        `batch ${b.batchId}: received(${b.received}) + returned(${b.returned}) != onHand(${b.onHand}) + shipped(${b.shipped})`,
      );
    }
    if (b.reserved < 0 || b.reserved > b.onHand) {
      throw new ConservationViolationError(
        `batch ${b.batchId}: reserved ${b.reserved} out of bounds [0, ${b.onHand}]`,
      );
    }
  }
  const t = view.totals;
  if (t.received + t.returned !== t.onHand + t.shipped) {
    throw new ConservationViolationError(
      `totals: received(${t.received}) + returned(${t.returned}) != onHand(${t.onHand}) + shipped(${t.shipped})`,
    );
  }
}

export class InventoryLedger {
  private events: InventoryEvent[] = [];
  private seq = 0;
  private inboundCounter = 0;
  private shipmentCounter = 0;
  private readonly batches = new Map<string, BatchState>();
  private readonly reservations = new Map<string, Reservation>();
  private readonly shipments = new Map<string, Shipment>();

  /** 入库。允许"迟到入库"(at 早于已提交的出库):只追加事件,不改写历史分配。 */
  inbound(cmd: { batchId: string; qty: number; expiryDate: string; at: string }): void {
    assertPositiveQty(cmd.qty, "inbound");
    const existing = this.batches.get(cmd.batchId);
    if (existing && existing.expiryDate !== cmd.expiryDate) {
      throw new BatchConflictError(
        `batch ${cmd.batchId} already exists with expiry ${existing.expiryDate}, got ${cmd.expiryDate}`,
      );
    }
    const inboundSeq = existing ? existing.inboundSeq : ++this.inboundCounter;
    const ev: InventoryEvent = {
      kind: "inbound",
      seq: ++this.seq,
      at: cmd.at,
      batchId: cmd.batchId,
      qty: cmd.qty,
      expiryDate: cmd.expiryDate,
      inboundSeq,
    };
    this.events.push(ev);
    applyEvent(this.batches, ev);
  }

  /** FEFO 排序键:先到期日,再入库序号。 */
  private fefoCandidates(at: string): BatchState[] {
    return [...this.batches.values()]
      .filter((b) => !isExpiredAt(b.expiryDate, at) && b.onHand - b.reserved > 0)
      .sort((x, y) => x.expiryDate.localeCompare(y.expiryDate) || x.inboundSeq - y.inboundSeq);
  }

  /** 预留:按 FEFO 计算分配并记录在事件上。库存不足则整体失败,不产生部分预留。 */
  reserve(orderId: string, qty: number, at: string): Allocation[] {
    assertPositiveQty(qty, "reserve");
    if (this.reservations.has(orderId)) {
      throw new DuplicateOrderError(`order ${orderId} already has a reservation`);
    }
    let need = qty;
    const allocations: Allocation[] = [];
    for (const b of this.fefoCandidates(at)) {
      if (need <= 0) break;
      const take = Math.min(b.onHand - b.reserved, need);
      allocations.push({ batchId: b.batchId, qty: take });
      need -= take;
    }
    if (need > 0) {
      throw new InsufficientInventoryError(
        `order ${orderId}: requested ${qty}, short by ${need} at ${at}`,
      );
    }
    const ev: InventoryEvent = {
      kind: "reserve",
      seq: ++this.seq,
      at,
      orderId,
      allocations,
    };
    this.events.push(ev);
    applyEvent(this.batches, ev);
    this.reservations.set(orderId, {
      orderId,
      status: "active",
      remaining: allocations.map((a) => ({ ...a })),
    });
    return allocations;
  }

  /** 取消预留:释放该订单剩余未出库的全部数量。 */
  cancelReservation(orderId: string, at: string): Allocation[] {
    const r = this.requireActiveReservation(orderId);
    const released = r.remaining.map((a) => ({ ...a }));
    const ev: InventoryEvent = {
      kind: "cancel",
      seq: ++this.seq,
      at,
      orderId,
      allocations: released,
    };
    this.events.push(ev);
    applyEvent(this.batches, ev);
    r.status = "cancelled";
    r.remaining = [];
    return released;
  }

  /** 出库:将预留转为已发货,批次归属在此刻冻结,之后任何迟到入库都不改写它。 */
  ship(orderId: string, at: string, qty?: number): { shipmentId: string; allocations: Allocation[] } {
    const r = this.requireActiveReservation(orderId);
    const remainingQty = sumQty(r.remaining);
    const shipQty = qty === undefined ? remainingQty : qty;
    assertPositiveQty(shipQty, "ship");
    if (shipQty > remainingQty) {
      throw new InsufficientInventoryError(
        `order ${orderId}: ship ${shipQty} exceeds reserved remaining ${remainingQty}`,
      );
    }
    let need = shipQty;
    const allocations: Allocation[] = [];
    for (const a of r.remaining) {
      if (need <= 0) break;
      const take = Math.min(a.qty, need);
      allocations.push({ batchId: a.batchId, qty: take });
      a.qty -= take;
      need -= take;
    }
    r.remaining = r.remaining.filter((a) => a.qty > 0);
    if (r.remaining.length === 0) r.status = "shipped";

    const shipmentId = `SHP-${++this.shipmentCounter}`;
    const ev: InventoryEvent = {
      kind: "ship",
      seq: ++this.seq,
      at,
      orderId,
      shipmentId,
      allocations,
    };
    this.events.push(ev);
    applyEvent(this.batches, ev);
    this.shipments.set(shipmentId, {
      shipmentId,
      orderId,
      at,
      allocations: allocations.map((a) => ({ ...a })),
      returnedPerAlloc: allocations.map(() => 0),
    });
    return { shipmentId, allocations };
  }

  /** 退货:按出库记录的批次归属,沿原分配顺序退回原批次。 */
  returnGoods(shipmentId: string, qty: number, at: string): Allocation[] {
    assertPositiveQty(qty, "return");
    const sh = this.shipments.get(shipmentId);
    if (!sh) throw new UnknownShipmentError(`shipment ${shipmentId} not found`);
    const shippedTotal = sumQty(sh.allocations);
    const returnedTotal = sh.returnedPerAlloc.reduce((acc, n) => acc + n, 0);
    if (qty > shippedTotal - returnedTotal) {
      throw new OverReturnError(
        `shipment ${shipmentId}: return ${qty} exceeds outstanding ${shippedTotal - returnedTotal}`,
      );
    }
    let need = qty;
    const applied: Allocation[] = [];
    for (let i = 0; i < sh.allocations.length && need > 0; i++) {
      const alloc = sh.allocations[i]!;
      const cap = alloc.qty - sh.returnedPerAlloc[i]!;
      const take = Math.min(cap, need);
      if (take <= 0) continue;
      sh.returnedPerAlloc[i]! += take;
      applied.push({ batchId: alloc.batchId, qty: take });
      need -= take;
    }
    const ev: InventoryEvent = {
      kind: "return",
      seq: ++this.seq,
      at,
      shipmentId,
      allocations: applied,
    };
    this.events.push(ev);
    applyEvent(this.batches, ev);
    return applied;
  }

  /** 历史时点库存:重放 at <= t 的事件(按 (at, seq) 排序),机械应用已记录分配。 */
  inventoryAt(t: string): InventoryView {
    const replay = this.events
      .filter((e) => e.at <= t)
      .sort((x, y) => (x.at < y.at ? -1 : x.at > y.at ? 1 : x.seq - y.seq));
    const batches = emptyBatches();
    for (const ev of replay) applyEvent(batches, ev);
    return buildView(batches, t);
  }

  /** 当前库存视图;asOf 缺省取最近事件的 at(用于过期判定)。 */
  inventoryNow(asOf?: string): InventoryView {
    const effective =
      asOf ?? this.events.reduce((acc, e) => (e.at > acc ? e.at : acc), "");
    return buildView(this.batches, effective);
  }

  /** 当前分配:活动预留及其批次归属。 */
  activeReservations(): ReservationView[] {
    return [...this.reservations.values()]
      .filter((r) => r.status === "active")
      .map((r) => ({ orderId: r.orderId, status: r.status, remaining: r.remaining.map((a) => ({ ...a })) }));
  }

  /** 当前状态守恒校验,通过则返回视图。 */
  assertConserved(asOf?: string): InventoryView {
    const view = this.inventoryNow(asOf);
    verifyView(view);
    return view;
  }

  private requireActiveReservation(orderId: string): Reservation {
    const r = this.reservations.get(orderId);
    if (!r || r.status !== "active") {
      throw new UnknownOrderError(`order ${orderId} has no active reservation`);
    }
    return r;
  }
}
