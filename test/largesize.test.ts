import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseInitSegment } from '../src/mp4.ts';
import { kidToBytes } from '../src/kid.ts';

const KID = '11111111222233334444555566667777';
const SYSTEM_ID = kidToBytes('edef8ba979d64acea3c827dcd51d21ed')!;

/** 手工构造 64 位 largesize 的 PSSH v1 盒子（size 字段=1）。 */
function largeSizePssh(): Uint8Array {
  const payloadLength = 4 + 16 + 4 + 16;
  const headerLength = 16;
  const out = new Uint8Array(headerLength + payloadLength);
  const view = new DataView(out.buffer);
  view.setUint32(0, 1); // largesize 标记
  out.set([0x70, 0x73, 0x73, 0x68], 4); // 'pssh'
  view.setUint32(8, 0);
  view.setUint32(12, out.length);
  out[16] = 1; // version
  out.set(SYSTEM_ID, 20);
  view.setUint32(36, 1);
  out.set(kidToBytes(KID)!, 40);
  return out;
}

test('64 位 largesize PSSH 盒子被正确解析且 raw 覆盖整盒', () => {
  const box = largeSizePssh();
  const parsed = parseInitSegment(box);
  assert.equal(parsed.psshBoxes.length, 1);
  assert.deepEqual(parsed.psshBoxes[0].kids, [KID]);
  assert.equal(parsed.psshBoxes[0].raw.length, box.length);
});
