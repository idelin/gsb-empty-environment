import {
  CartLine,
  Coupon,
  DiscountCoupon,
  ItemCoupon,
  LineStepDetail,
  RefundResult,
  Settlement,
  SettlementStep,
  ThresholdCoupon,
} from './types';

export const MAX_COUPONS = 20;

/** 券类型的默认结算顺序：单品券 -> 折扣券 -> 满减券 */
const TYPE_RANK: Record<Coupon['type'], number> = {
  ITEM: 0,
  DISCOUNT: 1,
  THRESHOLD: 2,
};

function assertPositiveInt(value: number, what: string): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${what} 必须是非负整数（分），收到: ${value}`);
  }
}

function validateCart(cart: CartLine[]): void {
  for (const line of cart) {
    if (!line.skuId) throw new Error('商品行缺少 skuId');
    assertPositiveInt(line.unitPrice, `商品 ${line.skuId} 的单价`);
    if (!Number.isInteger(line.quantity) || line.quantity <= 0) {
      throw new Error(`商品 ${line.skuId} 的数量必须是正整数`);
    }
  }
}

function validateCoupons(coupons: Coupon[]): void {
  if (coupons.length > MAX_COUPONS) {
    throw new Error(`券数量超过上限 ${MAX_COUPONS} 张`);
  }
  const ids = new Set<string>();
  for (const c of coupons) {
    if (!c.id) throw new Error('券缺少 id');
    if (ids.has(c.id)) throw new Error(`券 id 重复: ${c.id}`);
    ids.add(c.id);
    switch (c.type) {
      case 'ITEM': {
        const item = c as ItemCoupon;
        if (!item.skuId) throw new Error(`单品券 ${c.id} 缺少 skuId`);
        assertPositiveInt(item.amountOffPerUnit, `单品券 ${c.id} 的立减金额`);
        break;
      }
      case 'DISCOUNT': {
        const disc = c as DiscountCoupon;
        if (
          !Number.isInteger(disc.ratePercent) ||
          disc.ratePercent < 1 ||
          disc.ratePercent > 100
        ) {
          throw new Error(`折扣券 ${c.id} 的 ratePercent 必须在 1..100 之间`);
        }
        break;
      }
      case 'THRESHOLD': {
        const th = c as ThresholdCoupon;
        assertPositiveInt(th.threshold, `满减券 ${c.id} 的门槛`);
        assertPositiveInt(th.amountOff, `满减券 ${c.id} 的立减金额`);
        break;
      }
      default:
        throw new Error(`未知券类型: ${(c as Coupon).type}`);
    }
    for (const dep of c.dependsOn ?? []) {
      if (dep === c.id) throw new Error(`券 ${c.id} 不能依赖自身`);
      if (!ids.has(dep) && !coupons.some((x) => x.id === dep)) {
        throw new Error(`券 ${c.id} 依赖了不存在的券: ${dep}`);
      }
    }
  }
}

/**
 * 计算全局结算顺序：显式 dependsOn 为硬约束（拓扑序），
 * 同层内按 (类型顺序, id) 排序。任何子集都按此顺序过滤后依次结算。
 * 依赖成环时抛错（任何包含环的组合都不可能合法）。
 */
function globalOrder(coupons: Coupon[]): Coupon[] {
  const byId = new Map<string, Coupon>();
  for (const c of coupons) byId.set(c.id, c);

  const indegree = new Map<string, number>();
  const dependents = new Map<string, string[]>();
  for (const c of coupons) {
    indegree.set(c.id, 0);
    dependents.set(c.id, []);
  }
  for (const c of coupons) {
    for (const dep of c.dependsOn ?? []) {
      indegree.set(c.id, (indegree.get(c.id) as number) + 1);
      (dependents.get(dep) as string[]).push(c.id);
    }
  }

  const rank = (c: Coupon): number => TYPE_RANK[c.type];
  const less = (a: Coupon, b: Coupon): boolean =>
    rank(a) < rank(b) || (rank(a) === rank(b) && a.id < b.id);

  const available: Coupon[] = [];
  for (const c of coupons) {
    if (indegree.get(c.id) === 0) available.push(c);
  }
  available.sort((a, b) => (less(a, b) ? -1 : less(b, a) ? 1 : 0));

  const result: Coupon[] = [];
  while (available.length > 0) {
    const next = available.shift() as Coupon;
    result.push(next);
    for (const depId of dependents.get(next.id) as string[]) {
      const d = byId.get(depId) as Coupon;
      const deg = (indegree.get(depId) as number) - 1;
      indegree.set(depId, deg);
      if (deg === 0) {
        let i = 0;
        while (i < available.length && less(available[i], d)) i++;
        available.splice(i, 0, d);
      }
    }
  }
  if (result.length !== coupons.length) {
    throw new Error('券的先后依赖存在环，无法结算');
  }
  return result;
}

interface SimLine {
  skuId: string;
  quantity: number;
  remaining: number;
}

/**
 * 对选中的券（已按全局顺序给出）计算实付金额。
 * 任一券不可用（无适用商品 / 无优惠 / 门槛未达）返回 -1 表示组合非法。
 */
function evaluate(
  baseLines: SimLine[],
  scratch: SimLine[],
  ordered: Coupon[],
  mask: number,
  positions: number[],
): number {
  for (let i = 0; i < baseLines.length; i++) {
    scratch[i].remaining = baseLines[i].remaining;
  }
  let payable = 0;
  for (const l of baseLines) payable += l.remaining;

  for (let k = 0; k < ordered.length; k++) {
    if ((mask & (1 << positions[k])) === 0) continue;
    const coupon = ordered[k];

    if (coupon.type === 'ITEM') {
      const item = coupon as ItemCoupon;
      let discount = 0;
      for (const line of scratch) {
        if (line.skuId !== item.skuId || line.remaining <= 0) continue;
        const lineDiscount = Math.min(
          line.remaining,
          item.amountOffPerUnit * line.quantity,
        );
        line.remaining -= lineDiscount;
        discount += lineDiscount;
      }
      if (discount === 0) return -1;
      payable -= discount;
    } else if (coupon.type === 'DISCOUNT') {
      const disc = coupon as DiscountCoupon;
      const scope = disc.skuIds ? new Set(disc.skuIds) : null;
      let discount = 0;
      for (const line of scratch) {
        if (scope && !scope.has(line.skuId)) continue;
        if (line.remaining <= 0) continue;
        // 逐行向下取整：折后金额 = floor(行金额 * 折扣率 / 100)
        const after = Math.floor((line.remaining * disc.ratePercent) / 100);
        discount += line.remaining - after;
        line.remaining = after;
      }
      if (discount === 0) return -1;
      payable -= discount;
    } else {
      const th = coupon as ThresholdCoupon;
      if (payable < th.threshold) return -1; // 门槛未达，组合非法
      const discount = Math.min(th.amountOff, payable);
      if (discount === 0) return -1;
      payable -= discount;
    }
  }
  return payable;
}

/** 与 evaluate 相同的流程，但记录逐步明细（仅对最终胜出的组合调用一次） */
function simulateWithSteps(cart: CartLine[], ordered: Coupon[]): SettlementStep[] {
  const lines: SimLine[] = cart.map((l) => ({
    skuId: l.skuId,
    quantity: l.quantity,
    remaining: l.unitPrice * l.quantity,
  }));
  let payable = lines.reduce((sum, l) => sum + l.remaining, 0);
  const steps: SettlementStep[] = [];

  for (const coupon of ordered) {
    const amountBefore = payable;
    let discount = 0;
    const lineDetails: LineStepDetail[] = [];

    if (coupon.type === 'ITEM') {
      const item = coupon as ItemCoupon;
      for (const line of lines) {
        if (line.skuId !== item.skuId || line.remaining <= 0) continue;
        const lineDiscount = Math.min(
          line.remaining,
          item.amountOffPerUnit * line.quantity,
        );
        if (lineDiscount > 0) {
          line.remaining -= lineDiscount;
          discount += lineDiscount;
          lineDetails.push({
            skuId: line.skuId,
            quantity: line.quantity,
            amountBefore: line.remaining + lineDiscount,
            discount: lineDiscount,
            amountAfter: line.remaining,
          });
        }
      }
    } else if (coupon.type === 'DISCOUNT') {
      const disc = coupon as DiscountCoupon;
      const scope = disc.skuIds ? new Set(disc.skuIds) : null;
      for (const line of lines) {
        if (scope && !scope.has(line.skuId)) continue;
        if (line.remaining <= 0) continue;
        const after = Math.floor((line.remaining * disc.ratePercent) / 100);
        const lineDiscount = line.remaining - after;
        if (lineDiscount > 0) {
          line.remaining = after;
          discount += lineDiscount;
          lineDetails.push({
            skuId: line.skuId,
            quantity: line.quantity,
            amountBefore: after + lineDiscount,
            discount: lineDiscount,
            amountAfter: after,
          });
        }
      }
    } else {
      const th = coupon as ThresholdCoupon;
      discount = Math.min(th.amountOff, payable);
    }

    payable -= discount;
    steps.push({
      couponId: coupon.id,
      couponName: coupon.name,
      type: coupon.type,
      amountBefore,
      discount,
      amountAfter: payable,
      lines: lineDetails,
    });
  }
  return steps;
}

function popcount(x: number): number {
  let count = 0;
  let v = x;
  while (v !== 0) {
    v &= v - 1;
    count++;
  }
  return count;
}

/**
 * 求最优结算：枚举所有合法券组合，取实付最少者；
 * 并列时取使用券数最少者；再并列取券 id 序列字典序最小者（保证结果确定）。
 */
export function settle(cart: CartLine[], coupons: Coupon[]): Settlement {
  validateCart(cart);
  validateCoupons(coupons);

  const originalTotal = cart.reduce((s, l) => s + l.unitPrice * l.quantity, 0);
  const n = coupons.length;
  const ordered = globalOrder(coupons);
  const positions = new Map<string, number>();
  coupons.forEach((c, i) => positions.set(c.id, i));
  const orderPos = ordered.map((c) => positions.get(c.id) as number);

  // 互斥组掩码
  const groupMasks = new Map<string, number>();
  coupons.forEach((c, i) => {
    if (c.mutexGroup) {
      groupMasks.set(c.mutexGroup, (groupMasks.get(c.mutexGroup) ?? 0) | (1 << i));
    }
  });
  const groups = Array.from(groupMasks.values());

  // 依赖掩码：所选券的依赖必须全部在子集内
  const idIndex = new Map<string, number>();
  coupons.forEach((c, i) => idIndex.set(c.id, i));
  const depMasks = coupons.map((c) => {
    let mask = 0;
    for (const dep of c.dependsOn ?? []) {
      mask |= 1 << (idIndex.get(dep) as number);
    }
    return mask;
  });

  const baseLines: SimLine[] = cart.map((l) => ({
    skuId: l.skuId,
    quantity: l.quantity,
    remaining: l.unitPrice * l.quantity,
  }));
  const scratch: SimLine[] = cart.map((l) => ({
    skuId: l.skuId,
    quantity: l.quantity,
    remaining: 0,
  }));

  const keyOf = (mask: number): string => {
    const ids: string[] = [];
    for (let i = 0; i < n; i++) {
      if (mask & (1 << i)) ids.push(coupons[i].id);
    }
    return JSON.stringify(ids.sort());
  };

  // 初始最优为「不用任何券」的基线
  let bestMask = 0;
  let bestPayable = originalTotal;
  let bestCount = 0;
  let bestKey = keyOf(0);

  const total = 1 << n;
  for (let mask = 0; mask < total; mask++) {
    let ok = true;
    for (const g of groups) {
      if (popcount(mask & g) > 1) {
        ok = false;
        break;
      }
    }
    if (!ok) continue;
    for (let i = 0; i < n; i++) {
      if (mask & (1 << i)) {
        if ((depMasks[i] & ~mask) !== 0) {
          ok = false;
          break;
        }
      }
    }
    if (!ok) continue;

    const payable = evaluate(baseLines, scratch, ordered, mask, orderPos);
    if (payable < 0) continue;

    const count = popcount(mask);
    if (payable < bestPayable || (payable === bestPayable && count < bestCount)) {
      bestMask = mask;
      bestPayable = payable;
      bestCount = count;
      bestKey = keyOf(mask);
    } else if (payable === bestPayable && count === bestCount) {
      // 并列：取券 id 排序后字典序最小者，保证结果确定
      const key = keyOf(mask);
      if (key < bestKey) {
        bestMask = mask;
        bestKey = key;
      }
    }
  }

  const selected = ordered.filter((c) => bestMask & (1 << (positions.get(c.id) as number)));
  const steps = bestMask === 0 ? [] : simulateWithSteps(cart, selected);
  return {
    originalTotal,
    payable: bestPayable,
    totalDiscount: originalTotal - bestPayable,
    couponIds: selected.map((c) => c.id),
    steps,
  };
}

/**
 * 退一件（或多件）商品：从购物车移除后整体重新计算最优组合，
 * 而不是按原券优惠比例退款。
 */
export function refundItem(
  cart: CartLine[],
  coupons: Coupon[],
  skuId: string,
  quantity = 1,
): RefundResult {
  if (!Number.isInteger(quantity) || quantity <= 0) {
    throw new Error('退货数量必须是正整数');
  }
  const before = settle(cart, coupons);

  const newCart: CartLine[] = [];
  let remaining = quantity;
  for (const line of cart) {
    if (line.skuId === skuId && remaining > 0) {
      const deduct = Math.min(line.quantity, remaining);
      remaining -= deduct;
      if (line.quantity - deduct > 0) {
        newCart.push({ ...line, quantity: line.quantity - deduct });
      }
    } else {
      newCart.push({ ...line });
    }
  }
  if (remaining > 0) {
    throw new Error(`购物车中商品 ${skuId} 的数量不足 ${quantity} 件`);
  }

  const after = settle(newCart, coupons);
  return {
    skuId,
    quantity,
    refundAmount: before.payable - after.payable,
    before,
    after,
  };
}
