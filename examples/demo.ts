import {
  formatFen,
  formatSettlement,
  settle,
  settleAfterReturn,
  type CartItem,
  type Coupon,
} from '../src/index.ts';

const cart: CartItem[] = [
  { id: 'shirt', name: '衬衫', unitPrice: 19900, quantity: 1 },
  { id: 'pants', name: '长裤', unitPrice: 12950, quantity: 2 },
  { id: 'socks', name: '袜子', unitPrice: 999, quantity: 3 },
];

const coupons: Coupon[] = [
  { id: 'item1', name: '长裤单品券 每件减10元', type: 'item', itemId: 'pants', amountOff: 1000 },
  { id: 'disc1', name: '全场 9 折', type: 'discount', percent: 90, mutexGroup: 'store' },
  { id: 'disc2', name: '全场 8.5 折', type: 'discount', percent: 85, mutexGroup: 'store' },
  { id: 'th1', name: '满 300 减 40', type: 'threshold', threshold: 30000, off: 4000 },
  {
    id: 'th2',
    name: '加码：满 200 再减 15（需先用满300减40）',
    type: 'threshold',
    threshold: 20000,
    off: 1500,
    requires: ['th1'],
  },
];

const s = settle(cart, coupons);
console.log('=== 最优组合 ===');
console.log(formatSettlement(s));

const r = settleAfterReturn(cart, coupons, 'pants', 1);
console.log('\n=== 退掉 1 件长裤后重算 ===');
console.log(formatSettlement(r.afterReturn));
console.log(`应退金额: ¥${formatFen(r.refund)}`);
