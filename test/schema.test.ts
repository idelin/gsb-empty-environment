import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encode, decode, validate, ValidationError,
  WireReader, WireWriter, WireType,
} from '../src/index.ts';
import type { Schema } from '../src/index.ts';

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

const sample = {
  id: 0xffff_ffff_ffff_ffffn,
  track: {
    name: '晨跑',
    points: [{ x: -1, y: 2 }, { x: 300, y: -400 }],
    samples: [0, 127, 128, 16384, 0xffffffff],
  },
  blob: new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
};

test('nested message + packed repeated round-trip', () => {
  const restored = decode(Session, encode(Session, sample));
  assert.equal(restored.id, sample.id);
  assert.equal(restored.blob instanceof Uint8Array, true);
  assert.deepEqual([...(restored.blob as Uint8Array)], [...sample.blob]);
  const track = restored.track as Record<string, unknown>;
  assert.equal(track.name, '晨跑');
  assert.deepEqual(track.points, sample.track.points);
  assert.deepEqual(track.samples, sample.track.samples);
});

test('packed encoding is actually length-delimited on the wire', () => {
  const bytes = encode(Track, { points: [], samples: [1, 2, 3] });
  // field 3, LEN: tag = (3<<3)|2 = 0x1a, len 3, payload 01 02 03
  assert.deepEqual([...bytes], [0x1a, 0x03, 0x01, 0x02, 0x03]);
});

test('decoder accepts both packed and unpacked repeated encodings', () => {
  const unpacked = new Uint8Array([0x18, 0x01, 0x18, 0x02, 0x18, 0x03]); // field3 varint x3
  const packed = new Uint8Array([0x1a, 0x03, 0x01, 0x02, 0x03]);
  const S: Schema = [{ no: 3, name: 'v', type: 'uint32', repeated: true, packed: true }];
  assert.deepEqual(decode(S, unpacked).v, [1, 2, 3]);
  assert.deepEqual(decode(S, packed).v, [1, 2, 3]);
});

test('forward compat: unknown fields are skipped', () => {
  const V2: Schema = [
    ...Session,
    { no: 9, name: 'extra', type: 'string' },
    { no: 10, name: 'count', type: 'fixed32' },
  ];
  const bytes = encode(V2, { ...sample, extra: 'future-field', count: 42 });
  const restored = decode(Session, bytes); // v1 读取 v2 数据
  assert.equal(restored.id, sample.id);
  assert.equal((restored.track as Record<string, unknown>).name, '晨跑');
  assert.equal('extra' in restored, false);
});

test('backward compat: missing fields fall back to schema defaults', () => {
  const V2: Schema = [
    ...Session,
    { no: 9, name: 'extra', type: 'string', default: 'n/a' },
    { no: 10, name: 'tags', type: 'uint32', repeated: true },
  ];
  const restored = decode(V2, encode(Session, sample)); // v2 读取 v1 数据
  assert.equal(restored.extra, 'n/a');
  assert.deepEqual(restored.tags, []);
  assert.equal(restored.id, sample.id);
});

test('scalar defaults for absent fields', () => {
  const S: Schema = [
    { no: 1, name: 'a', type: 'uint32' },
    { no: 2, name: 'b', type: 'sint64' },
    { no: 3, name: 'c', type: 'string' },
    { no: 4, name: 'd', type: 'bool' },
    { no: 5, name: 'e', type: 'double' },
  ];
  assert.deepEqual(decode(S, new Uint8Array(0)), { a: 0, b: 0n, c: '', d: false, e: 0 });
});

test('reflection validation rejects bad values before encoding', () => {
  assert.throws(() => encode(Point, { x: 2 ** 31, y: 0 }), ValidationError); // sint32 溢出
  assert.throws(() => encode(Point, { x: 1.5, y: 0 }), ValidationError);     // 非整数
  assert.throws(() => encode(Point, { x: '1', y: 0 }), ValidationError);     // 类型错误
  assert.throws(() => encode(Session, { id: -1n }), ValidationError);        // uint64 负数
  assert.throws(() => encode(Track, { name: 'a', points: {}, samples: [] }), ValidationError); // repeated 非数组
  assert.throws(() => encode(Track, { name: 'a', points: [{ x: 0, y: 0 }], samples: [-1] }), ValidationError);
});

test('validate reports nested path', () => {
  try {
    validate(Session, { id: 1n, track: { name: 't', points: [{ x: 0, y: 'bad' }], samples: [] }, blob: new Uint8Array() });
    assert.unreachable();
  } catch (e) {
    assert.ok(e instanceof ValidationError);
    assert.match((e as InstanceType<typeof ValidationError>).path, /\$\.track\.points\[0\]\.y/);
  }
});

test('empty repeated fields encode to nothing', () => {
  const bytes = encode(Track, { name: 'x', points: [], samples: [] });
  assert.deepEqual(decode(Track, bytes), { name: 'x', points: [], samples: [] });
});

test('mismatched wire type on a known field is tolerated by skipping', () => {
  // field 1 (uint64) 被错误地编码为 fixed32
  const w = new WireWriter().tag(1, WireType.Fixed32).fixed32(7).tag(3, WireType.LengthDelimited).bytes(new Uint8Array([9]));
  const out = decode(Session, w.finish());
  assert.equal(out.id, 0n);          // 回退默认
  assert.deepEqual([...(out.blob as Uint8Array)], [9]); // 后续字段不受影响
});

test('float/double fields round-trip', () => {
  const S: Schema = [
    { no: 1, name: 'f', type: 'float' },
    { no: 2, name: 'd', type: 'double' },
  ];
  const out = decode(S, encode(S, { f: 1.5, d: Math.PI }));
  assert.equal(out.f, 1.5);
  assert.equal(out.d, Math.PI);
});

test('non-packable type marked packed is rejected by validation', () => {
  const S: Schema = [{ no: 1, name: 's', type: 'string', repeated: true, packed: true }];
  assert.throws(() => encode(S, { s: ['a'] }), ValidationError);
});

test('reader window constructor guards invalid ranges', () => {
  assert.throws(() => new WireReader(new Uint8Array(4), 2, 1));
  assert.throws(() => new WireReader(new Uint8Array(4), 0, 5));
});
