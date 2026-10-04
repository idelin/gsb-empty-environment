/**
 * 阶梯计费核心库。
 *
 * 模型约定：
 * - 用量按自然月（UTC）累计，采用边际阶梯定价：累计量落入哪个阶梯，该部分就按该阶梯单价计费。
 * - 事件可迟到（recordedAt 晚于账期关闭）、可撤回（retract 引用原事件 id）。
 * - 费率有版本，事件适用哪个版本由其「生效时间 effectiveAt」决定，与到达时间无关。
 * - 同一账期内若费率版本发生切换，每个事件使用自己版本的阶梯表，
 *   但累计位置（落在第几阶梯）由全账期按 effectiveAt 排序后的累计量决定。
 * - 账单一旦开出不可修改；重算后如有差额，追加一张调整账单（ADJUSTMENT）。
 * - 不变式：某账期所有账单（原始 + 全部调整）的净额 == 基于当前有效事件的一次性正确结算。
 *
 * 金额一律用整数分（cents），用量为非负整数单位。
 */

// ---------------------------------------------------------------- 类型

export interface Tier {
  /** 该阶梯累计用量上限（不含），最后一个阶梯为 Infinity */
  upTo: number;
  /** 该阶梯单价，单位：分 / 用量单位 */
  unitPriceCents: number;
}

export interface RateVersion {
  version: string;
  /** 该版本生效起始时间（epoch ms，含） */
  effectiveFrom: number;
  tiers: Tier[];
}

export interface UsageEvent {
  id: string;
  customerId: string;
  /** 用量实际生效时间，决定所属账期与费率版本 */
  effectiveAt: number;
  /** 用量，正整数 */
  quantity: number;
  /** 到达系统的时间（可晚于账期关闭） */
  recordedAt: number;
}

export interface LineItem {
  rateVersion: string;
  tierIndex: number;
  quantity: number;
  unitPriceCents: number;
  amountCents: number;
}

export type BillKind = "ORIGINAL" | "ADJUSTMENT";

export interface Bill {
  billId: string;
  customerId: string;
  /** 账期，格式 "YYYY-MM"（UTC） */
  month: string;
  kind: BillKind;
  /** 该账期内的账单序号，从 1 开始 */
  seq: number;
  items: LineItem[];
  totalCents: number;
  issuedAt: number;
  reason?: string;
}

// ---------------------------------------------------------------- 工具

/** 返回账期 [startMs, endMs)，UTC */
export function monthRange(month: string): { startMs: number; endMs: number } {
  const m = /^(\d{4})-(\d{2})$/.exec(month);
  if (!m) throw new Error(`invalid month: ${month}`);
  const year = Number(m[1]);
  const mon = Number(m[2]);
  if (mon < 1 || mon > 12) throw new Error(`invalid month: ${month}`);
  return {
    startMs: Date.UTC(year, mon - 1, 1),
    endMs: Date.UTC(year, mon, 1),
  };
}

export function monthOf(epochMs: number): string {
  const d = new Date(epochMs);
  const mm = String(d.getUTCMonth() + 1).padStart(2, "0");
  return `${d.getUTCFullYear()}-${mm}`;
}

/** 按生效时间定位费率版本（取 effectiveFrom <= t 的最后一个版本） */
export function rateVersionAt(versions: RateVersion[], t: number): RateVersion {
  const sorted = [...versions].sort((a, b) => a.effectiveFrom - b.effectiveFrom);
  let picked: RateVersion | undefined;
  for (const v of sorted) {
    if (v.effectiveFrom <= t) picked = v;
  }
  if (!picked) throw new Error(`no rate version effective at ${t}`);
  return picked;
}

// ---------------------------------------------------------------- 一次性结算

/**
 * 一次性正确结算：给定某账期当前全部有效事件，按 effectiveAt 排序后
 * 走阶梯累计，返回按 (费率版本, 阶梯) 聚合的明细行。
 * 这是重算与差额账单共同的「真相来源」。
 */
export function settle(
  events: UsageEvent[],
  month: string,
  rateVersions: RateVersion[]
): LineItem[] {
  const { startMs, endMs } = monthRange(month);
  const inMonth = events.filter(
    (e) => e.effectiveAt >= startMs && e.effectiveAt < endMs
  );
  // 排序需确定性：effectiveAt 相同则按 recordedAt，再按 id
  inMonth.sort(
    (a, b) =>
      a.effectiveAt - b.effectiveAt ||
      a.recordedAt - b.recordedAt ||
      (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
  );

  const items = new Map<string, LineItem>();
  let cumulative = 0;

  for (const e of inMonth) {
    if (!(e.quantity > 0)) throw new Error(`event ${e.id}: quantity must be > 0`);
    const rate = rateVersionAt(rateVersions, e.effectiveAt);
    let remaining = e.quantity;
    for (let i = 0; i < rate.tiers.length && remaining > 0; i++) {
      const tier = rate.tiers[i]!;
      const tierStart = i === 0 ? 0 : rate.tiers[i - 1]!.upTo;
      const room = tier.upTo - Math.max(cumulative, tierStart);
      if (room <= 0) continue;
      const take = Math.min(remaining, room);
      const key = `${rate.version}#${i}`;
      const prev = items.get(key);
      if (prev) {
        prev.quantity += take;
        prev.amountCents += take * tier.unitPriceCents;
      } else {
        items.set(key, {
          rateVersion: rate.version,
          tierIndex: i,
          quantity: take,
          unitPriceCents: tier.unitPriceCents,
          amountCents: take * tier.unitPriceCents,
        });
      }
      cumulative += take;
      remaining -= take;
    }
    if (remaining > 0) throw new Error(`event ${e.id}: tiers do not cover usage`);
  }

  return [...items.values()].sort(
    (a, b) =>
      (a.rateVersion < b.rateVersion ? -1 : a.rateVersion > b.rateVersion ? 1 : 0) ||
      a.tierIndex - b.tierIndex
  );
}

// ---------------------------------------------------------------- 差额计算

function itemKey(it: LineItem): string {
  return `${it.rateVersion}#${it.tierIndex}#${it.unitPriceCents}`;
}

/** newItems - baseItems，按 (版本, 阶梯, 单价) 聚合差额，可为负 */
export function diffItems(newItems: LineItem[], baseItems: LineItem[]): LineItem[] {
  const acc = new Map<string, LineItem>();
  const add = (it: LineItem, sign: 1 | -1) => {
    const key = itemKey(it);
    const prev = acc.get(key);
    if (prev) {
      prev.quantity += sign * it.quantity;
      prev.amountCents += sign * it.amountCents;
    } else {
      acc.set(key, {
        rateVersion: it.rateVersion,
        tierIndex: it.tierIndex,
        quantity: sign * it.quantity,
        unitPriceCents: it.unitPriceCents,
        amountCents: sign * it.amountCents,
      });
    }
  };
  newItems.forEach((it) => add(it, 1));
  baseItems.forEach((it) => add(it, -1));
  return [...acc.values()]
    .filter((it) => it.quantity !== 0 || it.amountCents !== 0)
    .sort(
      (a, b) =>
        (a.rateVersion < b.rateVersion ? -1 : a.rateVersion > b.rateVersion ? 1 : 0) ||
        a.tierIndex - b.tierIndex
    );
}

// ---------------------------------------------------------------- 计费引擎

interface Retraction {
  eventId: string;
  recordedAt: number;
}

export class BillingEngine {
  private readonly rateVersions: RateVersion[];
  private readonly events = new Map<string, UsageEvent>();
  private readonly retractions = new Map<string, Retraction>(); // key: 被撤回事件 id
  private readonly bills = new Map<string, Bill[]>(); // key: `${customerId}|${month}`
  private billCounter = 0;

  constructor(rateVersions: RateVersion[]) {
    if (rateVersions.length === 0) throw new Error("at least one rate version required");
    this.rateVersions = [...rateVersions].sort((a, b) => a.effectiveFrom - b.effectiveFrom);
  }

  /** 记录用量事件（允许迟到：effectiveAt 可落在已出账的账期） */
  recordEvent(event: UsageEvent): void {
    if (this.events.has(event.id)) throw new Error(`duplicate event id: ${event.id}`);
    if (!(event.quantity > 0)) throw new Error(`event ${event.id}: quantity must be > 0`);
    this.events.set(event.id, { ...event });
  }

  /** 撤回事件。幂等：重复撤回同一事件不报错也不重复生效 */
  retractEvent(eventId: string, recordedAt: number): void {
    if (!this.events.has(eventId)) throw new Error(`unknown event id: ${eventId}`);
    if (this.retractions.has(eventId)) return;
    this.retractions.set(eventId, { eventId, recordedAt });
  }

  /** 当前有效事件（未撤回的） */
  effectiveEvents(customerId: string): UsageEvent[] {
    return [...this.events.values()].filter(
      (e) => e.customerId === customerId && !this.retractions.has(e.id)
    );
  }

  /** 一次性正确结算（当前事件状态下的应有金额） */
  settleMonth(customerId: string, month: string): LineItem[] {
    return settle(this.effectiveEvents(customerId), month, this.rateVersions);
  }

  private billsFor(customerId: string, month: string): Bill[] {
    const key = `${customerId}|${month}`;
    let list = this.bills.get(key);
    if (!list) {
      list = [];
      this.bills.set(key, list);
    }
    return list;
  }

  /** 账期内已出账单（含调整）的净明细 */
  private netIssuedItems(customerId: string, month: string): LineItem[] {
    const all = this.billsFor(customerId, month).flatMap((b) => b.items);
    // diffItems(new, base) 的技巧：new=全部已出, base=[] 即得聚合净额
    return diffItems(all, []);
  }

  /** 关闭账期并开出原始账单 */
  closeMonth(customerId: string, month: string, issuedAt: number): Bill {
    const existing = this.billsFor(customerId, month);
    if (existing.length > 0) {
      throw new Error(`month ${month} already billed for ${customerId}`);
    }
    const items = this.settleMonth(customerId, month);
    const bill: Bill = {
      billId: `B${++this.billCounter}`,
      customerId,
      month,
      kind: "ORIGINAL",
      seq: 1,
      items,
      totalCents: items.reduce((s, it) => s + it.amountCents, 0),
      issuedAt,
    };
    existing.push(bill);
    return bill;
  }

  /**
   * 重算账期。若与已出账单净额有差额，追加一张调整账单并返回；
   * 无差额返回 null。账期尚未出账时抛错（应走 closeMonth）。
   */
  recalculate(
    customerId: string,
    month: string,
    issuedAt: number,
    reason?: string
  ): Bill | null {
    const existing = this.billsFor(customerId, month);
    if (existing.length === 0) {
      throw new Error(`month ${month} not billed yet for ${customerId}`);
    }
    const correct = this.settleMonth(customerId, month);
    const delta = diffItems(correct, this.netIssuedItems(customerId, month));
    if (delta.length === 0) return null;
    const bill: Bill = {
      billId: `B${++this.billCounter}`,
      customerId,
      month,
      kind: "ADJUSTMENT",
      seq: existing.length + 1,
      items: delta,
      totalCents: delta.reduce((s, it) => s + it.amountCents, 0),
      issuedAt,
      reason,
    };
    existing.push(bill);
    return bill;
  }

  /** 某账期全部账单（原始 + 调整，按开出顺序） */
  listBills(customerId: string, month: string): Bill[] {
    return [...this.billsFor(customerId, month)];
  }

  /** 某账期已出账单净额（原始 + 全部调整之和） */
  netBilledCents(customerId: string, month: string): number {
    return this.billsFor(customerId, month).reduce((s, b) => s + b.totalCents, 0);
  }

  /** 一次性正确结算的应有总额 */
  correctTotalCents(customerId: string, month: string): number {
    return this.settleMonth(customerId, month).reduce((s, it) => s + it.amountCents, 0);
  }
}
