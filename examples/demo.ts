/**
 * 运行样例：
 *  1. 反射 schema 编码三层嵌套 + packed repeated 消息，再解码还原
 *  2. 模拟断电/磁盘满/丢包，演示安全回滚与崩溃自愈
 * 运行：npm run demo
 */
import { decodeMessage, encodeMessage, type MessageSchema } from '../src/schema.ts';
import { FaultySink, LossyChannel, MemorySink } from '../src/fault.ts';
import { Journal } from '../src/journal.ts';
import { TransactionalStore, reliableTransfer } from '../src/store.ts';

const Address: MessageSchema = {
  name: 'Address',
  fields: [
    { number: 1, name: 'city', type: 'string', required: true },
    { number: 2, name: 'zip', type: 'uint32' },
  ],
};
const Order: MessageSchema = {
  name: 'Order',
  fields: [
    { number: 1, name: 'id', type: 'uint64', required: true },
    { number: 2, name: 'city', type: 'message', message: Address },
    { number: 3, name: 'amounts', type: 'sint64', repeated: true, packed: true },
    { number: 4, name: 'note', type: 'string' },
  ],
};

console.log('=== 1. 嵌套消息 + packed repeated 编解码 ===');
const order = {
  id: 18446744073709551615n,
  city: { city: '杭州', zip: 310000 },
  amounts: [-100n, 0n, 9999999999n],
  note: '首单 🎉',
};
const bytes = encodeMessage(Order, order);
console.log(`编码后 ${bytes.length} 字节:`, Buffer.from(bytes).toString('hex'));
const decoded = decodeMessage(Order, bytes);
console.log('解码还原:', JSON.stringify(decoded, (_, v) => (typeof v === 'bigint' ? `${v}n` : v)));

console.log('\n=== 2. 截断防护 ===');
try {
  decodeMessage(Order, bytes.subarray(0, bytes.length - 3));
} catch (err) {
  console.log('截断被安全拦截:', (err as Error).message);
}

console.log('\n=== 3. 断电自愈 ===');
const sink = new FaultySink(new MemorySink());
const store = new TransactionalStore(new Journal(sink));
await store.commit([{ kind: 'put', key: 'order:1', value: bytes }]);
sink.plan.powerLossAfterBytes = 5;
try {
  await store.commit([{ kind: 'put', key: 'order:2', value: bytes }]);
} catch (err) {
  console.log('断电拦截:', (err as Error).message);
}
console.log('内存中 order:2 =', store.get('order:2'), '(未应用半截事务)');
const store2 = new TransactionalStore(new Journal(sink));
console.log(`崩溃恢复：重放 ${store2.recover()} 条完好事务，order:1 存在 =`, store2.get('order:1') !== undefined);

console.log('\n=== 4. 磁盘满回滚 ===');
sink.plan.diskFull = true;
try {
  await store2.commit([{ kind: 'del', key: 'order:1' }]);
} catch (err) {
  console.log('磁盘满拦截:', (err as Error).message);
}
console.log('回滚后 order:1 仍在 =', store2.get('order:1') !== undefined, '| staged 泄漏 =', store2.stagedBufferCount);

console.log('\n=== 5. 丢包重传 ===');
const channel = new LossyChannel([2, 5]);
const { chunks, received } = await reliableTransfer(bytes, 7, channel);
console.log(`分片 ${chunks} 个，丢包 ${channel.stats.dropped} 次，重传后一致 =`, Buffer.compare(Buffer.from(bytes), Buffer.from(received)) === 0);
