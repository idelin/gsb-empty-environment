import test from 'node:test';
import assert from 'node:assert/strict';
import { toElements } from '../src/elements.ts';
import { layoutOptimal } from '../src/dp.ts';
import { layoutWithEdit } from '../src/incremental.ts';
import { GLYPHS, SHORT, word } from '../src/fixtures.ts';

test('增量编辑: 重用未受影响前沿, 成本与全量重算一致', () => {
  const inc = layoutWithEdit(SHORT.paragraph, 6, word('hippopotamus'), SHORT.config, GLYPHS.hyphenWidth);
  assert.ok(inc.verified, '证书第 5 步校验应通过');
  assert.ok(inc.reusedStates > 0, '应重用至少一个前沿状态');
  assert.ok(inc.recomputedStates > 0, '应重算受影响后缀');
  assert.ok(inc.certificate.length >= 5);
  assert.match(inc.certificate[0], /编辑定位/);
  assert.match(inc.certificate[1], /重用 DP 前沿状态/);
  assert.match(inc.certificate[4], /一致/);

  const nodes = [...SHORT.paragraph.nodes];
  nodes[6] = word('hippopotamus');
  const full = layoutOptimal(toElements({ nodes }, GLYPHS.hyphenWidth), SHORT.config);
  assert.ok(Math.abs(inc.result.cost - full.cost) <= 1e-9);
});

test('增量编辑: 段首编辑时无可重用前沿仍正确', () => {
  const inc = layoutWithEdit(SHORT.paragraph, 0, word('extraordinary'), SHORT.config, GLYPHS.hyphenWidth);
  assert.ok(inc.verified);
  const nodes = [...SHORT.paragraph.nodes];
  nodes[0] = word('extraordinary');
  const full = layoutOptimal(toElements({ nodes }, GLYPHS.hyphenWidth), SHORT.config);
  assert.ok(Math.abs(inc.result.cost - full.cost) <= 1e-9);
});
