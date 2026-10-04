import { WireWriter } from '../src/protobuf/writer.ts';
import { WireType } from '../src/protobuf/reader.ts';
import { zigzagEncode } from '../src/protobuf/varint.ts';
import { decodeMessage, validateMessage, type MessageSchema } from '../src/protobuf/schema.ts';
import { MultiTenantScheduler } from '../src/scheduler/scheduler.ts';

const println = (s: string): void => console.log(s);

// ---------------------------------------------------------------------------
// 第一部分：Protobuf Wire-format 手工编解码
// ---------------------------------------------------------------------------
println('=== Part 1: Protobuf Wire-format 解析 ===');

const Phone: MessageSchema = {
  name: 'Phone',
  fields: {
    1: { name: 'number', type: 'string' },
    2: { name: 'type', type: 'sint32' },
  },
};

const Person: MessageSchema = {
  name: 'Person',
  fields: {
    1: { name: 'name', type: 'string' },
    2: { name: 'id', type: 'int32' },
    3: { name: 'email', type: 'string' },
    4: { name: 'phones', type: Phone, repeated: true },
    5: { name: 'scores', type: 'sint32', repeated: true, packed: true },
    6: { name: 'balance', type: 'sint64' },
    7: { name: 'vip', type: 'bool' },
  },
};

const enc = new TextEncoder();
const w = new WireWriter();
w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(enc.encode('张三'));
w.writeTag(2, WireType.Varint).writeVarint(1001);
w.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(enc.encode('zhangsan@example.com'));

const phone1 = new WireWriter()
  .writeTag(1, WireType.LengthDelimited).writeLengthDelimited(enc.encode('138-0000-0000'))
  .writeTag(2, WireType.Varint).writeVarint(zigzagEncode(0))
  .finish();
const phone2 = new WireWriter()
  .writeTag(1, WireType.LengthDelimited).writeLengthDelimited(enc.encode('021-8888-8888'))
  .writeTag(2, WireType.Varint).writeVarint(zigzagEncode(-1))
  .finish();
w.writeTag(4, WireType.LengthDelimited).writeLengthDelimited(phone1);
w.writeTag(4, WireType.LengthDelimited).writeLengthDelimited(phone2);

const packedScores = new WireWriter();
for (const s of [90, -5, 82, 100]) packedScores.writeVarint(zigzagEncode(s));
w.writeTag(5, WireType.LengthDelimited).writeLengthDelimited(packedScores.finish());

w.writeTag(6, WireType.Varint).writeVarint(zigzagEncode(-9_000_000_000n));
w.writeTag(7, WireType.Varint).writeVarint(1);

const buf = w.finish();
println('编码后字节（' + buf.length + ' 字节）: ' + Buffer.from(buf).toString('hex'));

const decoded = decodeMessage(Person, buf);
println('解码结果:');
console.dir(decoded, { depth: null });

// 截断攻击演示：把 packed 数组声明长度改大 / 直接砍掉尾部
const truncated = buf.subarray(0, buf.length - 3);
const verdict = validateMessage(Person, truncated);
println('截断流校验: ok=' + verdict.ok + ' -> ' + verdict.errors[0]);

// ---------------------------------------------------------------------------
// 第二部分：多租户隔离 + DRR 加权公平调度
// ---------------------------------------------------------------------------
println('');
println('=== Part 2: 多租户 DRR 公平调度 ===');

let clock = 0;
const scheduler = new MultiTenantScheduler({
  baseQuantum: 1,
  maxInflight: 3,
  now: () => clock,
});
scheduler.registerTenant({ id: 'alice', weight: 1 });
scheduler.registerTenant({ id: 'bob', weight: 2 });
scheduler.registerTenant({ id: 'carol', weight: 1, quotaTokens: 3, refillIntervalMs: 1000 });

const dispatchLog: string[] = [];
const gates: Array<() => void> = [];
const makeJob = (label: string) => () => {
  dispatchLog.push(label);
  return new Promise<string>((resolve) =>
    gates.push(() => resolve(label)),
  );
};

const all: Promise<string>[] = [];
for (let i = 1; i <= 6; i++) all.push(scheduler.submit('alice', 1, makeJob('alice#' + i)));
for (let i = 1; i <= 6; i++) all.push(scheduler.submit('bob', 1, makeJob('bob#' + i)));
for (let i = 1; i <= 6; i++) all.push(scheduler.submit('carol', 1, makeJob('carol#' + i)));

const flush = (): Promise<void> => new Promise((r) => setImmediate(r));
await flush();
println('全局容量 3，首批派发: ' + dispatchLog.join(', '));

// carol 配额只有 3，前 3 个任务完成后配额耗尽，需要推进时钟补充
let released = 0;
while (gates.length > 0 || dispatchLog.length < 18) {
  if (gates.length === 0) {
    clock += 1000;
    scheduler.tick();
    await flush();
    if (gates.length === 0) break;
  }
  const batch = gates.splice(0);
  for (const g of batch) g();
  released += batch.length;
  await flush();
}

const results = await Promise.all(all);
println('派发顺序: ' + dispatchLog.join(' -> '));
println('完成数: ' + results.length + '（alice/bob 按 1:2 加权，carol 受配额 3/1000ms 限流）');
for (const id of ['alice', 'bob', 'carol']) {
  const st = scheduler.stats(id);
  println(
    '  ' + id + ': submitted=' + st.submitted + ' completed=' + st.completed +
    ' failed=' + st.failed + ' rejected=' + st.rejected,
  );
}
await scheduler.drain();
scheduler.assertNoLeaks();
println('drain 完成，泄漏检测通过：无残留队列 / 在途任务 / 未兑现等待者');
