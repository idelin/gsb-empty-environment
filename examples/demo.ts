/**
 * 端到端演示:
 *  1. 反射引擎编解码多层嵌套 + packed repeated 的 Protobuf 消息
 *  2. 注入突发断电, 验证崩溃恢复与安全回滚
 *  3. 注入磁盘满, 验证事务原子性
 *  4. 注入 40% 丢包, 验证可靠传输重组
 */

import { ReflectionEngine, type MessageSchema } from '../src/schema.ts';
import { FaultInjector } from '../src/faults.ts';
import { FaultyBlockDevice } from '../src/device.ts';
import { DurableStore } from '../src/store.ts';
import { LossyLink, sendReliable } from '../src/channel.ts';
import { InjectedFaultError, TruncatedFieldError } from '../src/errors.ts';

const show = (label: string, v: unknown) =>
  console.log(`\n=== ${label} ===\n`, v);

// ---------- 1. 编解码 ----------
const schema: MessageSchema = {
  name: 'Order',
  fields: [
    { name: 'orderId', number: 1, type: 'uint64', required: true },
    { name: 'buyer', number: 2, type: 'string', required: true },
    {
      name: 'items', number: 3, type: 'message', repeated: true,
      message: {
        name: 'Item',
        fields: [
          { name: 'sku', number: 1, type: 'string', required: true },
          { name: 'price', number: 2, type: 'sint64' },
          { name: 'attrs', number: 3, type: 'sint64', repeated: true, packed: true },
        ],
      },
    },
    { name: 'weights', number: 4, type: 'fixed32', repeated: true, packed: true },
  ],
};

const engine = new ReflectionEngine();
const order = {
  orderId: 9007199254740993n,
  buyer: '张三',
  items: [
    { sku: 'A-1', price: -199n, attrs: [1n, -2n, 300n] },
    { sku: 'B-2', price: 5999n, attrs: [] },
  ],
  weights: [250, 1024],
};
const bytes = engine.encode(schema, order);
show('1. Protobuf 编码 (hex)', Buffer.from(bytes).toString('hex'));
show('1. 解码还原', engine.decode(schema, bytes));

// 截断防护演示
try {
  engine.decode(schema, bytes.subarray(0, bytes.length - 3));
} catch (err) {
  show('1. 截断字段 -> 安全越界防护', (err as TruncatedFieldError).message);
}

// ---------- 2. 断电恢复 ----------
const injector = new FaultInjector(20261004);
const device = new FaultyBlockDevice(1024 * 1024, injector, 0.4);
const store = DurableStore.create(device);

await store.transact([{ key: 'order:1', value: bytes }]);
injector.arm('power-loss', { fireOnCalls: [2] }); // 第 2 次写盘时断电
try {
  await store.transact([{ key: 'order:2', value: bytes }]);
} catch (err) {
  show('2. 突发断电', (err as InjectedFaultError).message);
}
injector.disarmAll();
show('2. 断电后内存态 (已回滚)', store.keys());

const { store: recovered, report } = DurableStore.recover(device.reboot());
show('2. 重启崩溃恢复', {
  存活键: recovered.keys(),
  重放事务: report.committed.length,
  回滚事务: report.rolledBack,
  截断撕裂字节: report.truncatedBytes,
});

// ---------- 3. 磁盘满 ----------
injector.arm('disk-full', { probability: 1.0 });
try {
  await store.transact([{ key: 'order:3', value: bytes }]);
} catch (err) {
  show('3. 磁盘满', (err as InjectedFaultError).message);
}
injector.disarmAll();
show('3. 磁盘满后状态 (完整回滚)', store.keys());

// ---------- 4. 丢包传输 ----------
const lossy = new FaultInjector(7);
lossy.arm('packet-loss', { probability: 0.4 });
const link = new LossyLink(lossy);
const { received, stats } = sendReliable(link, bytes, 16);
show('4. 40% 丢包率下可靠传输', {
  逐字节一致: Buffer.from(received).equals(Buffer.from(bytes)),
  ...stats,
});
show('4. 重组后解码', engine.decode(schema, received));
