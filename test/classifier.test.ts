import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DSCP, dscpFromTos, tosFromDscp } from '../src/packet.ts';
import { classifyDscp, classifyTosByte } from '../src/classifier.ts';

test('extracts DSCP from the IP TOS byte', () => {
  assert.equal(dscpFromTos(0xb8), DSCP.EF); // 0xb8 = 101110_00
  assert.equal(dscpFromTos(0x00), DSCP.BE);
  assert.equal(dscpFromTos(0x68), DSCP.AF31); // 0x68 = 011010_00
  // ECN bits must not leak into the DSCP value.
  assert.equal(dscpFromTos(0xbb), DSCP.EF);
});

test('TOS byte round-trips through DSCP', () => {
  for (const dscp of [DSCP.BE, DSCP.EF, DSCP.AF11, DSCP.AF43]) {
    assert.equal(dscpFromTos(tosFromDscp(dscp)), dscp);
  }
});

test('rejects out-of-range header values', () => {
  assert.throws(() => dscpFromTos(256), RangeError);
  assert.throws(() => dscpFromTos(-1), RangeError);
  assert.throws(() => tosFromDscp(64), RangeError);
  assert.throws(() => classifyDscp(63.5), RangeError);
});

test('maps EF DSCP 46 to the EF queue', () => {
  assert.equal(classifyDscp(DSCP.EF), 'EF');
});

test('maps every AFxy codepoint to its AF class', () => {
  const expected: Record<number, string> = {
    [DSCP.AF11]: 'AF1', [DSCP.AF12]: 'AF1', [DSCP.AF13]: 'AF1',
    [DSCP.AF21]: 'AF2', [DSCP.AF22]: 'AF2', [DSCP.AF23]: 'AF2',
    [DSCP.AF31]: 'AF3', [DSCP.AF32]: 'AF3', [DSCP.AF33]: 'AF3',
    [DSCP.AF41]: 'AF4', [DSCP.AF42]: 'AF4', [DSCP.AF43]: 'AF4',
  };
  for (const [dscp, queue] of Object.entries(expected)) {
    assert.equal(classifyDscp(Number(dscp)), queue);
  }
});

test('maps CS0 and unassigned codepoints to BE', () => {
  for (const dscp of [0, 1, 8, 16, 47, 63]) {
    assert.equal(classifyDscp(dscp), 'BE', `dscp ${dscp}`);
  }
});

test('classifies directly from a raw TOS byte', () => {
  assert.equal(classifyTosByte(0xb8), 'EF');
  assert.equal(classifyTosByte(0x00), 'BE');
});
