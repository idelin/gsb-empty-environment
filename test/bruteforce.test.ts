import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applicationOrder,
  evaluate,
  settle,
  type CartItem,
  type Coupon,
} from '../src/index.ts';

// 简易可复现随机数
function mulberry32(seed: number) {
  return () => {
    seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function bruteForce(cart: CartItem[], coupons: Coupon[]) {
  let best = evaluate(cart, []);
  const n = coupons.length;
  for (let mask = 1; mask < 1 << n; mask++) {
    const chosen: Coupon[] = [];
    const groups = new Set<string>();
    let ok = true;
    for (let i = 0; i < n && ok; i++) {
      if (!(mask & (1 << i))) continue;
      const c = coupons[i];
      if (c.mutexGroup && groups.has(c.mutexGroup)) { ok = false; break; }
      if (c.mutexGroup) groups.add(c.mutexGroup);
      chosen.push(c);
    }
    if (!ok) continue;
    const ids = new Set(chosen.map((c) => c.id));
    if (!chosen.every((c) => (c.requires ?? []).every((r) => ids.has(r)))) continue;
    const s = evaluate(cart, applicationOrder(chosen));
    if (
      s.payable < best.payable ||
      (s.payable === best.payable && s.coupons.length < best.coupons.length)
    ) {
      best = s;
    }
  }
  return best;
}

test('随机对拍：与朴素枚举结果一致', () => {
  const rand = mulberry32(42);
  for (let round = 0; round < 60; round++) {
    const itemCount = 1 + Math.floor(rand() * 3);
    const cart: CartItem[] = Array.from({ length: itemCount }, (_, i) => ({
      id: `item${i}`,
      name: `item${i}`,
      unitPrice: 1 + Math.floor(rand() * 20000),
      quantity: 1 + Math.floor(rand() * 4),
    }));
    const couponCount = 2 + Math.floor(rand() * 7);
    const coupons: Coupon[] = Array.from({ length: couponCount }, (_, i) => {
      const kind = rand();
      const base = {
        id: `c${i}`,
        name: `c${i}`,
        mutexGroup: rand() < 0.35 ? `g${Math.floor(rand() * 3)}` : undefined,
        requires: i > 0 && rand() < 0.3 ? [`c${Math.floor(rand() * i)}`] : undefined,
        itemIds: rand() < 0.3 ? [cart[Math.floor(rand() * itemCount)].id] : undefined,
      };
      if (kind < 0.34) {
        return { ...base, type: 'item', itemId: cart[Math.floor(rand() * itemCount)].id, amountOff: 1 + Math.floor(rand() * 3000) };
      }
      if (kind < 0.67) {
        return { ...base, type: 'discount', percent: 1 + Math.floor(rand() * 100) };
      }
      return { ...base, type: 'threshold', threshold: 1 + Math.floor(rand() * 30000), off: 1 + Math.floor(rand() * 5000) };
    });
    const expected = bruteForce(cart, coupons);
    const actual = settle(cart, coupons);
    assert.equal(actual.payable, expected.payable, `round ${round} payable`);
    assert.equal(actual.coupons.length, expected.coupons.length, `round ${round} count`);
  }
});
