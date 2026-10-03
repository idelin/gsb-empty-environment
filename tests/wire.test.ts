import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader, WireWriter, WireType } from '../src/wire.ts';
import { TruncatedFieldError, WireFormatError } from '../src/errors.ts';

function buildNestedMessage(): Uint8Array {
  // 三层嵌套: Outer{ inner: Middle{ inner: Inner{ value: -42 } }, ids: packed[1,2,300], name }
  const w = new WireWriter();
  w.writeMessage(1, (m) => {
    m.writeMessage(1, (inner) => {
      inner.writeSInt64(1, -42n);
    });
    m.writeString(2, 'middle');
  });
  w.writePackedVarints(2, [1n, 2n, 300n]);
  w.writeFixed32(3, 0xdeadbeef);
  w.writeFixed64(4, 0x0123456789abcdefn);
  w.writeBool(5, true);
  return w.finish();
}

test('手工解析三层嵌套消息并还原字段', () => {
  const bytes = buildNestedMessage();
  const r = new WireReader(bytes);

  let h = r.readFieldHeader();
  assert.deepEqual(h, { fieldNumber: 1, wireType: WireType.LengthDelimited });
  const middle = r.readSubMessage();
  const mh1 = middle.readFieldHeader();
  assert.equal(mh1.fieldNumber, 1);
  const inner = middle.readSubMessage();
  const ih = inner.readFieldHeader();
  assert.equal(ih.fieldNumber, 1);
  assert.equal(inner.readSInt64(), -42n);
  assert.ok(inner.done);
  const mh2 = middle.readFieldHeader();
  assert.equal(mh2.fieldNumber, 2);
  assert.equal(middle.readString(), 'middle');
  assert.ok(middle.done);

  h = r.readFieldHeader();
  assert.deepEqual(h, { fieldNumber: 2, wireType: WireType.LengthDelimited });
  const packed = new WireReader(r.readBytes());
  assert.deepEqual(
    [packed.readVarint(), packed.readVarint(), packed.readVarint()],
    [1n, 2n, 300n],
  );
  assert.ok(packed.done);

  h = r.readFieldHeader();
  assert.equal(h.wireType, WireType.Fixed32);
  assert.equal(r.readFixed32(), 0xdeadbeef);

  h = r.readFieldHeader();
  assert.equal(h.wireType, WireType.Fixed64);
  assert.equal(r.readFixed64(), 0x0123456789abcdefn);

  h = r.readFieldHeader();
  assert.equal(r.readBool(), true);
  assert.ok(r.done);
});

test('字段截断: 字段内部截断必触发安全越界防护, 绝不越界读取', () => {
  const full = buildNestedMessage();
  let guardHits = 0;
  let cleanPrefixes = 0;
  for (let cut = 0; cut < full.length; cut++) {
    const truncated = full.subarray(0, cut);
    const r = new WireReader(truncated);
    try {
      while (!r.done) {
        const h = r.readFieldHeader();
        r.skipField(h.wireType);
      }
      cleanPrefixes++; // 恰好落在字段边界: 合法的更短消息
    } catch (err) {
      assert.ok(
        err instanceof TruncatedFieldError || err instanceof WireFormatError,
        `cut=${cut} 应为越界防护错误, 实际: ${err}`,
      );
      guardHits++;
    }
  }
  assert.ok(guardHits > 0, '字段内部截断必须被防护捕获');
  // 明确验证: 截断在第一个 length-delimited 字段载荷中间必抛 TruncatedFieldError
  const r = new WireReader(full.subarray(0, 3));
  r.readFieldHeader();
  assert.throws(() => r.skipField(WireType.LengthDelimited), TruncatedFieldError);
  // 完整流则全部解析成功
  const ok = new WireReader(full);
  while (!ok.done) {
    const h = ok.readFieldHeader();
    ok.skipField(h.wireType);
  }
});

test('length-delimited 声明长度超过剩余字节 -> TruncatedFieldError', () => {
  const w = new WireWriter();
  w.writeString(1, 'hello world');
  const full = w.finish();
  const r = new WireReader(full.subarray(0, 4)); // 头+长度在, 载荷被截断
  r.readFieldHeader();
  assert.throws(() => r.readBytes(), TruncatedFieldError);
});

test('fixed32/fixed64 截断防护', () => {
  const w = new WireWriter();
  w.writeFixed64(1, 1n);
  const full = w.finish();
  const r = new WireReader(full.subarray(0, 5)); // tag + 4 字节, 不足 8
  r.readFieldHeader();
  assert.throws(() => r.readFixed64(), (err: unknown) => {
    assert.ok(err instanceof TruncatedFieldError);
    assert.equal((err as TruncatedFieldError).needed, 8);
    assert.equal((err as TruncatedFieldError).remaining, 4);
    return true;
  });
});

test('非法字段号 0 与未知 wire type 被拒绝', () => {
  assert.throws(() => new WireReader(Uint8Array.from([0x00])).readFieldHeader(),
    WireFormatError);
  // wire type 3 (group, 不支持)
  assert.throws(() => new WireReader(Uint8Array.from([0x0b])).readFieldHeader(),
    WireFormatError);
});

test('未知字段安全跳过 (含嵌套 length-delimited)', () => {
  const w = new WireWriter();
  w.writeString(9, 'unknown-field');
  w.writeUint64(1, 42n);
  const r = new WireReader(w.finish());
  const h1 = r.readFieldHeader();
  assert.equal(h1.fieldNumber, 9);
  r.skipField(h1.wireType);
  const h2 = r.readFieldHeader();
  assert.equal(h2.fieldNumber, 1);
  assert.equal(r.readVarint(), 42n);
});

test('packed fixed32/fixed64 数组往返', () => {
  const w = new WireWriter();
  w.writePackedFixed32(1, [1, 2, 0xffffffff]);
  w.writePackedFixed64(2, [1n, (1n << 64n) - 1n]);
  const r = new WireReader(w.finish());
  r.readFieldHeader();
  const p32 = new WireReader(r.readBytes());
  assert.deepEqual([p32.readFixed32(), p32.readFixed32(), p32.readFixed32()],
    [1, 2, 0xffffffff]);
  r.readFieldHeader();
  const p64 = new WireReader(r.readBytes());
  assert.deepEqual([p64.readFixed64(), p64.readFixed64()],
    [1n, (1n << 64n) - 1n]);
});

test('float/double 往返 (含特殊值)', () => {
  const w = new WireWriter();
  w.writeFloat(1, 3.14);
  w.writeDouble(2, Math.PI);
  w.writeDouble(3, Infinity);
  w.writeDouble(4, NaN);
  const r = new WireReader(w.finish());
  r.readFieldHeader();
  assert.ok(Math.abs(r.readFloat() - 3.14) < 1e-6);
  r.readFieldHeader();
  assert.equal(r.readDouble(), Math.PI);
  r.readFieldHeader();
  assert.equal(r.readDouble(), Infinity);
  r.readFieldHeader();
  assert.ok(Number.isNaN(r.readDouble()));
});
