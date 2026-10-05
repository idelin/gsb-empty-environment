import type {
  CartItem,
  Coupon,
  DiscountCoupon,
  Fen,
  ItemCoupon,
  ReturnResult,
  Settlement,
  SettlementStep,
  ThresholdCoupon,
} from './types.ts';

/** 单笔结算最多参与的券数量 */
export const MAX_COUPONS = 20;

const TYPE_ORDER: Record<Coupon['type'], number> = {
  item: 0,
  discount: 1,
  threshold: 2,
};

function assertPositiveInt(value: number, what: string): void {
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`${what} 必须为正整数（分），收到: ${value}`);
  }
}

/** 校验购物车与券列表的合法性 */
export function validate(cart: CartItem[], coupons: Coupon[]): void {
  if (coupons.length > MAX_COUPONS) {
    throw new Error(`单笔结算最多 ${MAX_COUPONS} 张券，收到 ${coupons.length} 张`);
  }
  const itemIds = new Set<string>();
  for (const item of cart) {
    assertPositiveInt(item.unitPrice, `商品 ${item.id} 单价`);
    assertPositiveInt(item.quantity, `商品 ${item.id} 数量`);
    if (itemIds.has(item.id)) throw new Error(`商品 id 重复: ${item.id}`);
    itemIds.add(item.id);
  }
  const couponIds = new Set<string>();
  for (const c of coupons) {
    if (couponIds.has(c.id)) throw new Error(`券 id 重复: ${c.id}`);
    couponIds.add(c.id);
  }
  for (const c of coupons) {
    for (const req of c.requires ?? []) {
      if (!couponIds.has(req)) throw new Error(`券 ${c.id} 依赖不存在的券 ${req}`);
      if (req === c.id) throw new Error(`券 ${c.id} 不能依赖自身`);
    }
    for (const id of c.itemIds ?? []) {
      if (!itemIds.has(id)) throw new Error(`券 ${c.id} 适用了不存在的商品 ${id}`);
    }
    switch (c.type) {
      case 'item': {
        const ic = c as ItemCoupon;
        assertPositiveInt(ic.amountOff, `券 ${c.id} 立减金额`);
        if (!itemIds.has(ic.itemId)) throw new Error(`单品券 ${c.id} 指向不存在的商品 ${ic.itemId}`);
        break;
      }
      case 'discount': {
        const dc = c as DiscountCoupon;
        if (!Number.isInteger(dc.percent) || dc.percent < 1 || dc.percent > 100) {
          throw new Error(`折扣券 ${c.id} 的 percent 须在 1-100 之间，收到: ${dc.percent}`);
        }
        break;
      }
      case 'threshold': {
        const tc = c as ThresholdCoupon;
        assertPositiveInt(tc.threshold, `券 ${c.id} 门槛`);
        assertPositiveInt(tc.off, `券 ${c.id} 立减金额`);
        break;
      }
      default:
        throw new Error(`未知券类型: ${(c as Coupon).type}`);
    }
  }
}

/**
 * 结算顺序：单品券 → 折扣券 → 满减券；
 * 同类型内部按「先后关系」拓扑排序（前置券先结算），其余保持输入顺序。
 */
export function applicationOrder(coupons: Coupon[]): Coupon[] {
  const byType = new Map<Coupon['type'], Coupon[]>();
  for (const c of coupons) {
    const list = byType.get(c.type) ?? [];
    list.push(c);
    byType.set(c.type, list);
  }
  const ordered: Coupon[] = [];
  for (const type of ['item', 'discount', 'threshold'] as const) {
    ordered.push(...topoByRequires(byType.get(type) ?? []));
  }
  return ordered;
}

function topoByRequires(coupons: Coupon[]): Coupon[] {
  const inSet = new Set(coupons.map((c) => c.id));
  const done = new Set<string>();
  const result: Coupon[] = [];
  let progress = true;
  while (result.length < coupons.length && progress) {
    progress = false;
    for (const c of coupons) {
      if (done.has(c.id)) continue;
      const deps = (c.requires ?? []).filter((r) => inSet.has(r));
      if (deps.every((r) => done.has(r))) {
        done.add(c.id);
        result.push(c);
        progress = true;
      }
    }
  }
  if (result.length < coupons.length) {
    throw new Error('券的先后关系存在循环依赖');
  }
  return result;
}

interface Line {
  item: CartItem;
  remaining: Fen;
}

function scopeIndices(coupon: Coupon, lines: Line[]): number[] {
  const out: number[] = [];
  for (let i = 0; i < lines.length; i++) {
    if (!coupon.itemIds || coupon.itemIds.includes(lines[i].item.id)) out.push(i);
  }
  return out;
}

/** 对给定（已排序）的券组合逐张结算，返回逐步金额 */
export function evaluate(cart: CartItem[], orderedCoupons: Coupon[]): Settlement {
  const lines: Line[] = cart.map((item) => ({
    item,
    remaining: item.unitPrice * item.quantity,
  }));
  const totalBefore = lines.reduce((s, l) => s + l.remaining, 0);
  const steps: SettlementStep[] = [];

  for (const coupon of orderedCoupons) {
    const amountBefore = lines.reduce((s, l) => s + l.remaining, 0);
    const breakdown: Array<{ itemId: string; off: Fen }> = [];

    if (coupon.type === 'item') {
      const ic = coupon as ItemCoupon;
      const idx = lines.findIndex((l) => l.item.id === ic.itemId);
      const off = Math.min(ic.amountOff * lines[idx].item.quantity, lines[idx].remaining);
      lines[idx].remaining -= off;
      breakdown.push({ itemId: ic.itemId, off });
    } else if (coupon.type === 'discount') {
      const dc = coupon as DiscountCoupon;
      for (const i of scopeIndices(dc, lines)) {
        // 逐行向下取整
        const off = Math.floor((lines[i].remaining * (100 - dc.percent)) / 100);
        if (off > 0) {
          lines[i].remaining -= off;
          breakdown.push({ itemId: lines[i].item.id, off });
        }
      }
    } else {
      const tc = coupon as ThresholdCoupon;
      const idxs = scopeIndices(tc, lines);
      const applicable = idxs.reduce((s, i) => s + lines[i].remaining, 0);
      if (applicable >= tc.threshold) {
        let left = Math.min(tc.off, applicable);
        // 按行剩余比例分摊（向下取整），尾差补给最后一行，保证行金额守恒
        for (const i of idxs) {
          if (left <= 0) break;
          const share = Math.min(
            Math.floor((tc.off * lines[i].remaining) / applicable),
            lines[i].remaining,
            left,
          );
          if (share > 0) {
            lines[i].remaining -= share;
            left -= share;
            breakdown.push({ itemId: lines[i].item.id, off: share });
          }
        }
        for (let k = idxs.length - 1; k >= 0 && left > 0; k--) {
          const i = idxs[k];
          const add = Math.min(left, lines[i].remaining);
          lines[i].remaining -= add;
          left -= add;
          const entry = breakdown.find((b) => b.itemId === lines[i].item.id);
          if (entry) entry.off += add;
          else if (add > 0) breakdown.push({ itemId: lines[i].item.id, off: add });
        }
      }
    }

    const amountAfter = lines.reduce((s, l) => s + l.remaining, 0);
    steps.push({
      couponId: coupon.id,
      couponName: coupon.name,
      type: coupon.type,
      amountBefore,
      discount: amountBefore - amountAfter,
      amountAfter,
      lineBreakdown: breakdown,
    });
  }

  const payable = lines.reduce((s, l) => s + l.remaining, 0);
  return {
    totalBefore,
    totalDiscount: totalBefore - payable,
    payable,
    coupons: orderedCoupons,
    steps,
  };
}

/** 单张券独立作用于原始购物车的优惠上限（用于搜索剪枝的上界） */
function maxOff(coupon: Coupon, cart: CartItem[]): Fen {
  return evaluate(cart, [coupon]).totalDiscount;
}

function isLegal(chosen: Coupon[]): boolean {
  const groups = new Set<string>();
  const ids = new Set(chosen.map((c) => c.id));
  for (const c of chosen) {
    if (c.mutexGroup) {
      if (groups.has(c.mutexGroup)) return false;
      groups.add(c.mutexGroup);
    }
    for (const req of c.requires ?? []) {
      if (!ids.has(req)) return false;
    }
  }
  return true;
}

function better(a: Settlement, b: Settlement): boolean {
  if (a.payable !== b.payable) return a.payable < b.payable;
  if (a.coupons.length !== b.coupons.length) return a.coupons.length < b.coupons.length;
  const ka = a.coupons.map((c) => c.id).sort().join('');
  const kb = b.coupons.map((c) => c.id).sort().join('');
  return ka < kb;
}

/**
 * 求解实付最少的合法券组合；实付并列时选用券最少者。
 * 指数级枚举（券数 ≤ 20），带上界剪枝。
 */
export function solve(cart: CartItem[], coupons: Coupon[]): Settlement {
  validate(cart, coupons);
  const n = coupons.length;
  const upper = coupons.map((c) => maxOff(c, cart));
  const suffixUpper: Fen[] = new Array(n + 1).fill(0);
  for (let i = n - 1; i >= 0; i--) suffixUpper[i] = suffixUpper[i + 1] + upper[i];

  let best = evaluate(cart, []);

  const chosen: Coupon[] = [];
  const usedGroups = new Set<string>();

  function dfs(i: number, takenUpper: Fen): void {
    if (takenUpper + suffixUpper[i] < best.totalDiscount) return; // 上界剪枝
    if (i === n) {
      if (chosen.length === 0) return;
      if (!isLegal(chosen)) return;
      const result = evaluate(cart, applicationOrder(chosen));
      if (better(result, best)) best = result;
      return;
    }
    const c = coupons[i];
    const groupBlocked = c.mutexGroup !== undefined && usedGroups.has(c.mutexGroup);
    if (!groupBlocked) {
      chosen.push(c);
      if (c.mutexGroup) usedGroups.add(c.mutexGroup);
      dfs(i + 1, takenUpper + upper[i]);
      chosen.pop();
      if (c.mutexGroup) usedGroups.delete(c.mutexGroup);
    }
    dfs(i + 1, takenUpper);
  }
  dfs(0, 0);
  return best;
}

/** 结算入口：返回最优组合的逐步金额 */
export function settle(cart: CartItem[], coupons: Coupon[]): Settlement {
  return solve(cart, coupons);
}

/**
 * 退一件（或多件）商品后重算：
 * 从购物车移除相应数量，重新求解最优组合，退款额 = 原应付 - 新应付。
 * 不按比例折算已用券。
 */
export function settleAfterReturn(
  cart: CartItem[],
  coupons: Coupon[],
  itemId: string,
  quantity = 1,
): ReturnResult {
  const original = settle(cart, coupons);
  const target = cart.find((i) => i.id === itemId);
  if (!target) throw new Error(`购物车中不存在商品 ${itemId}`);
  if (!Number.isInteger(quantity) || quantity < 1 || quantity > target.quantity) {
    throw new Error(`退货数量非法: ${quantity}（持有 ${target.quantity}）`);
  }
  const newCart = cart
    .map((i) => (i.id === itemId ? { ...i, quantity: i.quantity - quantity } : i))
    .filter((i) => i.quantity > 0);
  const afterReturn = settle(newCart, coupons);
  return {
    original,
    afterReturn,
    refund: original.payable - afterReturn.payable,
    returned: { itemId, quantity },
  };
}

/** 分 → 「12.34」元字符串 */
export function formatFen(fen: Fen): string {
  const sign = fen < 0 ? '-' : '';
  const abs = Math.abs(fen);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`;
}

const TYPE_LABEL: Record<Coupon['type'], string> = {
  item: '单品券',
  discount: '折扣券',
  threshold: '满减券',
};

/** 生成人类可读的逐步结算单 */
export function formatSettlement(s: Settlement): string {
  const lines: string[] = [];
  lines.push(`优惠前总额: ¥${formatFen(s.totalBefore)}`);
  for (const step of s.steps) {
    lines.push(
      `  [${TYPE_LABEL[step.type]}] ${step.couponName} (${step.couponId})` +
        `  ¥${formatFen(step.amountBefore)} - ¥${formatFen(step.discount)} = ¥${formatFen(step.amountAfter)}`,
    );
    for (const b of step.lineBreakdown) {
      lines.push(`      ${b.itemId}: -¥${formatFen(b.off)}`);
    }
  }
  lines.push(`总优惠: ¥${formatFen(s.totalDiscount)}`);
  lines.push(`最终应付: ¥${formatFen(s.payable)}`);
  return lines.join('\n');
}
