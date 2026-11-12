import { test } from 'node:test';
import assert from 'node:assert/strict';

import { evaluateAdvisory, evaluateRange } from '../src/osv.ts';
import { parseSemVer } from '../src/semver.ts';
import type { OsvAdvisory, OsvAffectedRange } from '../src/types.ts';

function range(...events: OsvAffectedRange['events']): OsvAffectedRange {
  return { type: 'SEMVER', events };
}

test('introduced 为 0：从最早版本起受影响，直到 fixed 关闭', () => {
  const affectedRange = range({ introduced: '0' }, { fixed: '2.0.0' });
  assert.equal(evaluateRange(parse('0.0.1'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('1.9.9'), affectedRange), 'affected');
});

test('fixed 边界：fixed 版本本身不受影响', () => {
  const affectedRange = range({ introduced: '1.0.0' }, { fixed: '2.0.0' });
  assert.equal(evaluateRange(parse('1.0.0'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('1.5.0'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('2.0.0'), affectedRange), 'unaffected');
  assert.equal(evaluateRange(parse('2.1.0'), affectedRange), 'unaffected');
  assert.equal(evaluateRange(parse('0.9.0'), affectedRange), 'unaffected');
});

test('last_affected 边界：该版本本身仍受影响，之后才脱离', () => {
  const affectedRange = range({ introduced: '1.0.0' }, { last_affected: '3.0.0' });
  assert.equal(evaluateRange(parse('1.0.0'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('3.0.0'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('3.0.1'), affectedRange), 'unaffected');
});

test('同一范围事件按序评估，支持多个受影响窗口', () => {
  const affectedRange = range(
    { introduced: '0' },
    { fixed: '1.0.0' },
    { introduced: '2.0.0' },
    { fixed: '3.0.0' },
  );
  assert.equal(evaluateRange(parse('0.9.0'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('1.0.0'), affectedRange), 'unaffected');
  assert.equal(evaluateRange(parse('1.5.0'), affectedRange), 'unaffected');
  assert.equal(evaluateRange(parse('2.0.0'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('2.9.9'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('3.0.0'), affectedRange), 'unaffected');
});

test('预发布版本小于正式版：fixed 边界的预发布判定', () => {
  const fixedRange = range({ introduced: '0' }, { fixed: '2.0.0' });
  assert.equal(evaluateRange(parse('2.0.0-rc.1'), fixedRange), 'affected');
  assert.equal(evaluateRange(parse('2.0.0'), fixedRange), 'unaffected');

  const lastRange = range({ introduced: '0' }, { last_affected: '2.0.0-rc.1' });
  assert.equal(evaluateRange(parse('2.0.0-rc.1'), lastRange), 'affected');
  assert.equal(evaluateRange(parse('2.0.0-rc.2'), lastRange), 'unaffected');
  assert.equal(evaluateRange(parse('2.0.0'), lastRange), 'unaffected');
});

test('introduced 预发布边界按优先级包含', () => {
  const affectedRange = range({ introduced: '1.0.0-beta.1' }, { fixed: '1.0.0' });
  assert.equal(evaluateRange(parse('1.0.0-alpha'), affectedRange), 'unaffected');
  assert.equal(evaluateRange(parse('1.0.0-beta.1'), affectedRange), 'affected');
  assert.equal(evaluateRange(parse('1.0.0-beta.2'), affectedRange), 'affected');
});

test('无事件的范围视为不受影响；非 SEMVER 类型不可评估', () => {
  assert.equal(evaluateRange(parse('1.0.0'), { type: 'SEMVER', events: [] }), 'unaffected');
  assert.equal(
    evaluateRange(parse('1.0.0'), { type: 'GIT', events: [{ introduced: '0' }] }),
    'unevaluable',
  );
});

test('事件版本非法时范围不可评估', () => {
  const badRange = range({ introduced: '0' }, { fixed: 'not-semver' });
  assert.equal(evaluateRange(parse('1.0.0'), badRange), 'unevaluable');
});

test('evaluateAdvisory：任一 range 命中即受影响，并透出不可评估原因', () => {
  const advisory: OsvAdvisory = {
    id: 'OSV-TEST-0001',
    packageName: 'demo',
    ranges: [
      range({ introduced: '1.0.0' }, { fixed: '1.5.0' }),
      range({ introduced: '2.0.0' }, { last_affected: '2.3.0' }),
    ],
  };
  assert.deepEqual(evaluateAdvisory('1.2.0', advisory), {
    affected: true,
    unevaluable: false,
    unevaluableReason: undefined,
  });
  assert.equal(evaluateAdvisory('1.5.0', advisory).affected, false);
  assert.equal(evaluateAdvisory('2.3.0', advisory).affected, true);
  assert.equal(evaluateAdvisory('3.0.0', advisory).affected, false);

  const broken: OsvAdvisory = {
    id: 'OSV-TEST-0002',
    packageName: 'demo',
    ranges: [range({ introduced: '0' }, { fixed: 'nope' })],
  };
  const result = evaluateAdvisory('1.0.0', broken);
  assert.equal(result.unevaluable, true);
  assert.equal(result.unevaluableReason, 'invalid-advisory-event');
});

test('锁定版本自身非法时返回 invalid-version', () => {
  const advisory: OsvAdvisory = {
    id: 'OSV-TEST-0003',
    packageName: 'demo',
    ranges: [range({ introduced: '0' })],
  };
  assert.equal(evaluateAdvisory('garbage', advisory).unevaluableReason, 'invalid-version');
});

function parse(version: string) {
  return parseSemVer(version);
}
