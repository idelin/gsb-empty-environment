/**
 * 运行样例：
 * 1) 手工构造多层嵌套 + packed repeated 的 Protobuf 二进制流并反射解码还原；
 * 2) 模拟外部压力变化，展示自适应阈值伸缩与负载感知限流。
 */
import { decodeMessage, encodeMessage, type MessageSchema } from '../src/message.ts';
import { ByteWriter, WireType } from '../src/wire.ts';
import { AdaptiveThreshold, LoadAwareLimiter, ShedLoadError } from '../src/adaptive.ts';

// ---------- 1. Schema 定义（三层嵌套 + packed repeated） ----------
const Geo: MessageSchema = {
  name: 'Geo',
  fields: [
    { name: 'lat', number: 1, type: 'sint64' },
    { name: 'lng', number: 2, type: 'sint64' },
  ],
};
const Address: MessageSchema = {
  name: 'Address',
  fields: [
    { name: 'city', number: 1, type: 'string' },
    { name: 'geo', number: 2, type: 'message', message: Geo },
  ],
};
const Person: MessageSchema = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'uint64' },
    { name: 'name', number: 2, type: 'string' },
    { name: 'scores', number: 3, type: 'sint32', repeated: true, packed: true },
    { name: 'address', number: 4, type: 'message', message: Address },
  ],
};

const person = {
  id: 18446744073709551615n, // uint64 最大值
  name: '李雷',
  scores: [-100, 0, 88, 2147483647],
  address: { city: '杭州', geo: { lat: 30274100n, lng: -120155100n } },
};

// 编码 -> 十六进制展示
const encoded = encodeMessage(Person, person);
console.log('== Protobuf Wire-format 编解码 ==');
console.log(`编码后 ${encoded.length} 字节:`);
console.log(Buffer.from(encoded).toString('hex').replace(/(..)/g, '$1 ').trim());

// 纯手工再构造一次（不经过 encodeMessage），验证解析器与手写字节一致
const manual = new ByteWriter();
manual.writeTag(1, WireType.Varint).writeVarint(person.id);
manual.writeTag(2, WireType.LengthDelimited).writeString(person.name);
{
  const packed = new ByteWriter();
  for (const s of person.scores) packed.writeZigZag(s);
  manual.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(packed.concat());
}
{
  const geo = new ByteWriter();
  geo.writeTag(1, WireType.Varint).writeZigZag(person.address.geo.lat);
  geo.writeTag(2, WireType.Varint).writeZigZag(person.address.geo.lng);
  const addr = new ByteWriter();
  addr.writeTag(1, WireType.LengthDelimited).writeString(person.address.city);
  addr.writeTag(2, WireType.LengthDelimited).writeLengthDelimited(geo.concat());
  manual.writeTag(4, WireType.LengthDelimited).writeLengthDelimited(addr.concat());
}
const manualBytes = manual.concat();
console.log(`手工构造与编码器输出一致: ${Buffer.from(manualBytes).equals(Buffer.from(encoded))}`);

// 反射解码还原
const decoded = decodeMessage(Person, encoded);
console.log('解码还原:', JSON.stringify(decoded, (_, v) => (typeof v === 'bigint' ? `${v}n` : v), 2));

// 截断防护演示
try {
  decodeMessage(Person, encoded.subarray(0, encoded.length - 5));
} catch (e) {
  console.log(`截断防护触发: [${(e as Error).name}] ${(e as Error).message}`);
}

// ---------- 2. 自适应阈值 + 负载感知 ----------
console.log('\n== 自适应动态阈值伸缩与负载感知 ==');
const ctl = new AdaptiveThreshold({
  min: 1, max: 16, initial: 8, increaseStep: 2, decreaseFactor: 0.5, cooldownMs: 50,
});
const limiter = new LoadAwareLimiter({ threshold: ctl, maxQueue: 64 });

const phases = [
  { name: '低压（资源充裕）', metrics: { cpuLoad: 0.1, memoryUsage: 0.2, errorRate: 0, queueDepth: 0 } },
  { name: '中压（滞回区间）', metrics: { cpuLoad: 0.5, memoryUsage: 0.4, errorRate: 0, queueDepth: 3 } },
  { name: '高压（雪崩风险）', metrics: { cpuLoad: 0.97, memoryUsage: 0.9, errorRate: 0.3, queueDepth: 80 } },
];
for (const phase of phases) {
  for (let i = 0; i < 3; i++) {
    const snap = ctl.adjust(phase.metrics);
    console.log(
      `${phase.name} -> 压力=${snap.pressure.toFixed(2)} 阈值=${snap.threshold} 动作=${snap.direction}`,
    );
    await new Promise((r) => setTimeout(r, 60)); // 越过冷却期
  }
}

// 并发执行一批任务，观察限流与卸载
let done = 0;
let shed = 0;
await Promise.allSettled(
  Array.from({ length: 40 }, (_, i) =>
    limiter
      .run(async () => {
        await new Promise((r) => setTimeout(r, 5));
        done++;
      })
      .catch((e) => {
        if (e instanceof ShedLoadError) shed++;
        else throw e;
      }),
  ),
);
const stats = limiter.stats;
console.log(`任务完成=${done} 卸载=${shed} 完成计数=${stats.completed} 当前阈值=${stats.limit}`);
limiter.assertDrained();
console.log('泄漏检查: inFlight=0, pending=0 ✔');
