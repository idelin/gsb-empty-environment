// 运行样例：多层嵌套消息 + Packed Repeated 的持久化编解码与零拷贝还原。
// 运行: node example/demo.ts   (Node >= 22.6，无需任何依赖)
import { encode, decode, TruncatedError } from '../src/index.ts';
import type { Schema } from '../src/index.ts';

// 模拟 proto 定义:
// message Point   { sint32 x = 1; sint32 y = 2; }
// message Track   { string name = 1; repeated Point points = 2;
//                   repeated uint32 samples = 3 [packed=true]; }
// message Session { uint64 id = 1; Track track = 2; bytes blob = 3; }
const Point: Schema = [
  { no: 1, name: 'x', type: 'sint32' },
  { no: 2, name: 'y', type: 'sint32' },
];
const Track: Schema = [
  { no: 1, name: 'name', type: 'string' },
  { no: 2, name: 'points', type: 'message', repeated: true, schema: Point },
  { no: 3, name: 'samples', type: 'uint32', repeated: true, packed: true },
];
const Session: Schema = [
  { no: 1, name: 'id', type: 'uint64' },
  { no: 2, name: 'track', type: 'message', schema: Track },
  { no: 3, name: 'blob', type: 'bytes' },
];

const session = {
  id: 0xffff_ffff_ffff_ffffn,          // uint64 最大值
  track: {
    name: '晨跑-外滩',
    points: [
      { x: -1, y: 2 },
      { x: 300, y: -400 },
    ],
    samples: [0, 1, 127, 128, 16383, 16384, 0xffffffff], // varint 各长度边界
  },
  blob: new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
};

// 1) 持久化编码（反射校验 + 紧凑二进制）
const persisted = encode(Session, session);
console.log(`encoded ${persisted.length} bytes:`,
  Buffer.from(persisted).toString('hex'));

// 2) 零拷贝还原：bytes 字段是源缓冲区的视图，不发生复制
const restored = decode(Session, persisted);
console.log('restored:', JSON.stringify(restored, (_, v) =>
  typeof v === 'bigint' ? `${v}n` : v instanceof Uint8Array ? [...v] : v));
const blob = restored.blob as Uint8Array;
console.log('zero-copy blob view:',
  blob.buffer === persisted.buffer && blob.byteOffset === persisted.byteOffset + persisted.length - 4);

// 3) 截断防护：任意截断都必须抛出 TruncatedError，而不是越界或脏数据
let guarded = 0;
let validPrefix = 0;
for (let cut = 0; cut < persisted.length; cut++) {
  try {
    decode(Session, persisted.subarray(0, cut));
    validPrefix++; // 恰好落在字段边界：等价于一条更短的合法消息
  } catch (e) {
    if (e instanceof TruncatedError) guarded++;
    else throw e; // 除截断外的任何异常都视为防护失败
  }
}
console.log(`truncation guard: ${guarded} cuts rejected, ${validPrefix} cuts are valid shorter messages`);
