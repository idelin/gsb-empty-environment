/**
 * 运行样例：手工编码 → 反射解码 → 离线完整性巡检 → 截断防护演示。
 * 运行：node examples/demo.ts   （Node >= 23.6，原生 TypeScript 类型擦除）
 */
import {
  decodeMessage, encodeMessage,
  EntityGraph, IntegrityInspector, TruncationError,
} from '../src/index.ts';
import type { MessageDescriptor, DecodedMessage } from '../src/index.ts';

const Address: MessageDescriptor = {
  typeName: 'Address',
  fields: [
    { name: 'city', number: 1, kind: 'string' },
    { name: 'zip', number: 2, kind: 'fixed32' },
  ],
};

const Order: MessageDescriptor = {
  typeName: 'Order',
  fields: [
    { name: 'id', number: 1, kind: 'uint64', id: true },
    { name: 'userId', number: 2, kind: 'uint64', ref: 'User' },
    { name: 'amounts', number: 3, kind: 'sint32', repeated: true, packed: true },
    { name: 'address', number: 4, kind: 'message', message: Address },
  ],
};

const User: MessageDescriptor = {
  typeName: 'User',
  fields: [
    { name: 'id', number: 1, kind: 'uint64', id: true },
    { name: 'name', number: 2, kind: 'string' },
    { name: 'referrerId', number: 3, kind: 'uint64', ref: 'User' },
  ],
};

const toJson = (v: unknown) =>
  JSON.stringify(v, (_, x) => (typeof x === 'bigint' ? `${x}n` : x instanceof Uint8Array ? [...x] : x), 2);

// 1) 编码一条带嵌套消息与 packed repeated 的订单
const orderBuf = encodeMessage(Order, {
  id: 9007199254740993n,
  userId: 42n,
  amounts: [10, -20, 30, -2147483648],
  address: { city: '上海', zip: 200001 },
});
console.log('== 1. Wire-format 字节流 ==');
console.log(`len=${orderBuf.length} hex=${Buffer.from(orderBuf).toString('hex')}`);

// 2) 反射解码还原结构体
const order = decodeMessage(Order, orderBuf);
console.log('\n== 2. 反射解码还原 ==');
console.log(toJson(order));

// 3) 构建实体图并做离线完整性巡检
const graph = new EntityGraph();
graph.collect(Order, order as DecodedMessage);
// 正常用户 + 一个悬挂引用目标 + 一对互相引用但与主图断开的"幽灵用户"
graph.collect(User, { $type: 'User', $unknown: [], id: 42n, name: 'alice', referrerId: 7n } as DecodedMessage);
graph.collect(User, { $type: 'User', $unknown: [], id: 100n, name: 'ghost-a', referrerId: 101n } as DecodedMessage);
graph.collect(User, { $type: 'User', $unknown: [], id: 101n, name: 'ghost-b', referrerId: 100n } as DecodedMessage);
// 数据倾斜：大量订单指向同一热门用户
for (let i = 0; i < 12; i++) {
  graph.collect(Order, { $type: 'Order', $unknown: [], id: BigInt(1000 + i), userId: 42n } as DecodedMessage);
}
graph.addRoot('Order', 9007199254740993n);

const inspector = new IntegrityInspector(graph);
inspector.addInvariant('order-id-positive', (g) => {
  const violations = [];
  for (const e of g.entities.values()) {
    if (e.type === 'Order' && BigInt(e.id) <= 0n) violations.push({ entity: e.key, detail: 'non-positive order id' });
  }
  return violations;
});
const report = inspector.inspect();
console.log('\n== 3. 完整性巡检报告 ==');
console.log(toJson(report));

// 4) 截断防护：逐字节截断订单流，观察安全失败
console.log('\n== 4. 截断安全越界防护 ==');
const cut = orderBuf.slice(0, Math.floor(orderBuf.length / 2));
try {
  decodeMessage(Order, cut);
  console.log('半截缓冲意外解码成功（字段恰好在截断点前结束）');
} catch (e) {
  const err = e as TruncationError;
  console.log(`安全拒绝: ${err.name}: ${err.message} needed=${err.needed} available=${err.available}`);
}
