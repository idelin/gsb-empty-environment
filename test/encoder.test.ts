import { test } from 'node:test';
import assert from 'node:assert/strict';
import { encodePacket, decodePacket } from '../src/encoder.ts';
import { compress } from '../src/compressor.ts';
import { generateDriveCycle } from '../src/simulator.ts';
import { maxHeadingErrorDeg } from '../src/metrics.ts';

test('编解码往返：航向角逐位一致', () => {
  const kept = compress(generateDriveCycle());
  const decoded = decodePacket(encodePacket(kept));
  assert.equal(decoded.length, kept.length);
  for (let i = 0; i < kept.length; i++) {
    assert.equal(decoded[i].heading, kept[i].heading, 'heading must be bit-exact');
    assert.equal(decoded[i].t, kept[i].t);
  }
  assert.equal(maxHeadingErrorDeg(kept, decoded), 0);
});

test('编解码往返：全字段逐位一致（无损编码）', () => {
  const kept = compress(generateDriveCycle());
  const decoded = decodePacket(encodePacket(kept));
  for (let i = 0; i < kept.length; i++) {
    assert.equal(decoded[i].lat, kept[i].lat);
    assert.equal(decoded[i].lon, kept[i].lon);
    assert.equal(decoded[i].ele, kept[i].ele);
    assert.equal(decoded[i].speed, kept[i].speed);
  }
});

test('数据包体积压缩至原始数据的 12% 以内', () => {
  const raw = generateDriveCycle();
  const kept = compress(raw);
  const packet = encodePacket(kept);
  const rawBytes = Buffer.byteLength(JSON.stringify(raw));
  const ratio = packet.length / rawBytes;
  assert.ok(ratio <= 0.12, `byte ratio ${(ratio * 100).toFixed(2)}% exceeds 12%`);
});

test('空轨迹与单点轨迹', () => {
  assert.equal(decodePacket(encodePacket([])).length, 0);
  const one = [{ t: 1, lat: 31.2, lon: 121.5, ele: 5, speed: 0, heading: 270 }];
  const decoded = decodePacket(encodePacket(one));
  assert.equal(decoded.length, 1);
  assert.equal(decoded[0].heading, 270);
});

test('坏魔数拒绝解码', () => {
  const packet = encodePacket(compress(generateDriveCycle()));
  const corrupted = Uint8Array.from(packet);
  corrupted[0] ^= 0xff;
  assert.throws(() => decodePacket(corrupted), /magic/);
});
