import { MessageSchema } from '../src/schema.ts';
import { SparseVector, ConcurrentSparseVector } from '../src/sparse.ts';
import { encodeVarint, decodeVarint, zigzagEncode32 } from '../src/varint.ts';
import { ProtobufDecodeError } from '../src/errors.ts';

// ---------- 1. Varint / ZigZag 基础 ----------
console.log('=== Varint / ZigZag ===');
const enc = encodeVarint(300);
console.log('varint(300) =', [...enc].map((b) => b.toString(16)), '->', decodeVarint(enc).value);
console.log('zigzag(-1) =', zigzagEncode32(-1), ' zigzag(1) =', zigzagEncode32(1));

// ---------- 2. 反射模式：嵌套消息 + packed repeated ----------
console.log('\n=== 反射模式编解码 ===');
const Point = new MessageSchema('Point', [
  { name: 'x', number: 1, type: 'sint32' },
  { name: 'y', number: 2, type: 'sint32' },
]);
const Track = new MessageSchema('Track', [
  { name: 'id', number: 1, type: 'uint64' },
  { name: 'points', number: 2, type: 'message', repeated: true, packed: false, messageType: Point },
  { name: 'deltas', number: 3, type: 'sint64', repeated: true, packed: true },
]);

const msg = {
  id: 9007199254740993n,
  points: [{ x: -3, y: 5 }, { x: 8, y: -13 }],
  deltas: [0n, -1n, 2n, -3n],
};
const buf = Track.encode(msg);
console.log(`编码 ${buf.length} 字节:`, Buffer.from(buf).toString('hex'));
console.log('解码结果:', JSON.stringify(Track.decode(buf), (_, v) => (typeof v === 'bigint' ? `${v}n` : v)));

// ---------- 3. 截断的安全防护 ----------
console.log('\n=== 截断防护 ===');
try {
  Track.decode(buf.subarray(0, buf.length - 3));
} catch (e) {
  if (e instanceof ProtobufDecodeError) console.log('安全拦截:', e.message);
}

// ---------- 4. 稀疏数据优化器 ----------
console.log('\n=== 稀疏数据优化器 ===');
const v = new SparseVector();
for (let i = 0; i < 500; i++) v.set(i * 7919, i + 1); // 跨度约 400 万，仅 500 项
console.log('稀疏态:', v.stats());
const packed = v.compress();
console.log(`压缩: ${v.size} 项 -> ${packed.length} 字节`);
console.log('解压校验一致:', [...SparseVector.decompress(packed).entries()].length === v.size);

const dense = new SparseVector();
for (let i = 0; i < 1000; i++) dense.set(i, i + 1);   // 密度 1.0 -> 自适应切换 dense 容器
console.log('稠密态:', dense.stats());

// ---------- 5. 并发安全 ----------
console.log('\n=== 并发写入 ===');
const cv = new ConcurrentSparseVector();
await Promise.all(Array.from({ length: 1000 }, (_, i) => cv.set(i, i * 3 + 1)));
console.log('1000 个并发 set 完成, size =', cv.inner.size, ' 抽查 v[999] =', cv.get(999));
