import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseInitSegment } from '../src/mp4.ts';
import { kidToBytes } from '../src/kid.ts';
import { extractKidsFromPssh } from '../src/validator.ts';
import {
  box,
  concat,
  ftypBox,
  initSegmentWithSinf,
  moovBox,
  psshV0,
  psshV1,
  trakBox,
  widevineKidField,
} from './helpers/boxes.ts';

const KID_A = '11111111222233334444555566667777';
const KID_B = 'aaaabbbbccccddddeeeeffff00001111';
const SYSTEM_ID = kidToBytes('edef8ba979d64acea3c827dcd51d21ed')!;

test('解析嵌套 sinf 中的 tenc KID 与 schm 方案', () => {
  const init = initSegmentWithSinf(kidToBytes(KID_A)!, 'cbcs');
  const parsed = parseInitSegment(init);
  assert.equal(parsed.tracks.length, 1);
  assert.equal(parsed.tracks[0].kid, KID_A);
  assert.equal(parsed.tracks[0].schemeType, 'cbcs');
  assert.equal(parsed.tracks[0].isProtected, true);
  assert.equal(parsed.tracks[0].perSampleIvSize, 8);
});

test('解析 PSSH v1 中的 KID 列表', () => {
  const pssh = psshV1(SYSTEM_ID, [kidToBytes(KID_A)!, kidToBytes(KID_B)!]);
  const parsed = parseInitSegment(concat(ftypBox(), pssh, moovBox()));
  assert.equal(parsed.psshBoxes.length, 1);
  assert.equal(parsed.psshBoxes[0].version, 1);
  assert.deepEqual(parsed.psshBoxes[0].kids, [KID_A, KID_B]);
});

test('解析 Widevine PSSH v0 中 protobuf 编码的 KID', () => {
  const data = concat(
    widevineKidField(kidToBytes(KID_A)!),
    widevineKidField(kidToBytes(KID_B)!),
  );
  const pssh = psshV0(SYSTEM_ID, data);
  const parsed = parseInitSegment(pssh);
  assert.equal(parsed.psshBoxes[0].version, 0);
  assert.deepEqual(parsed.psshBoxes[0].kids, []);
  // v0 本身无 KID 数组，由 validator 的 Widevine 解析补充。
  const kids = [...extractKidsFromPssh(parsed).keys()];
  assert.deepEqual(kids, [KID_A, KID_B]);
});

test('顶层与 moov 内的 PSSH 都能被收集', () => {
  const top = psshV1(SYSTEM_ID, [kidToBytes(KID_A)!]);
  const inner = psshV1(SYSTEM_ID, [kidToBytes(KID_B)!]);
  const parsed = parseInitSegment(concat(ftypBox(), top, moovBox(box('trak', inner))));
  assert.equal(parsed.psshBoxes.length, 2);
});

test('截断/恶意盒子被安全跳过而非抛异常', () => {
  const truncated = new Uint8Array([0, 0, 0, 20, 0x70, 0x73, 0x73, 0x68, 1, 0]);
  assert.doesNotThrow(() => parseInitSegment(truncated));
  assert.equal(parseInitSegment(truncated).tracks.length, 0);
});
