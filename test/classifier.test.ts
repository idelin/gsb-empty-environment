import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DscpClassifier } from '../src/classifier.ts';
import { DSCP } from '../src/packet.ts';
import type { QueueId } from '../src/packet.ts';

test('EF (DSCP 46) maps to the EF queue', () => {
  const c = new DscpClassifier();
  assert.equal(c.classifyDscp(DSCP.EF), 'EF');
});

test('all AFxy codepoints map to their AF class', () => {
  const c = new DscpClassifier();
  const cases: Array<[number, QueueId]> = [
    [DSCP.AF11, 'AF1'], [DSCP.AF12, 'AF1'], [DSCP.AF13, 'AF1'],
    [DSCP.AF21, 'AF2'], [DSCP.AF22, 'AF2'], [DSCP.AF23, 'AF2'],
    [DSCP.AF31, 'AF3'], [DSCP.AF32, 'AF3'], [DSCP.AF33, 'AF3'],
    [DSCP.AF41, 'AF4'], [DSCP.AF42, 'AF4'], [DSCP.AF43, 'AF4'],
  ];
  for (const [dscp, queue] of cases) {
    assert.equal(c.classifyDscp(dscp), queue, `DSCP ${dscp}`);
  }
});

test('CS0 and unknown codepoints fall back to BE', () => {
  const c = new DscpClassifier();
  assert.equal(c.classifyDscp(DSCP.CS0), 'BE');
  assert.equal(c.classifyDscp(1), 'BE');
  assert.equal(c.classifyDscp(63), 'BE');
  assert.equal(c.classifyDscp(DSCP.CS5), 'BE');
});

test('TOS byte: DSCP is the high 6 bits, ECN bits are ignored', () => {
  const c = new DscpClassifier();
  // EF with ECN = 0b101110_11 = 0xBB
  assert.equal(c.classifyTos(0b10111011), 'EF');
  // AF41 (34) with ECN = 0b100010_01 = 0x89
  assert.equal(c.classifyTos(0b10001001), 'AF4');
  assert.equal(c.classifyTos(0x00), 'BE');
});

test('rejects out-of-range DSCP / TOS values', () => {
  const c = new DscpClassifier();
  assert.throws(() => c.classifyDscp(-1), RangeError);
  assert.throws(() => c.classifyDscp(64), RangeError);
  assert.throws(() => c.classifyDscp(1.5), RangeError);
  assert.throws(() => c.classifyTos(256), RangeError);
});

test('operator overrides can re-map codepoints', () => {
  const c = new DscpClassifier(new Map<number, QueueId>([[DSCP.CS5, 'EF']]));
  assert.equal(c.classifyDscp(DSCP.CS5), 'EF');
  assert.equal(c.classifyDscp(DSCP.EF), 'EF');
});
