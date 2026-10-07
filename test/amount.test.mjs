import assert from 'node:assert/strict';
import { test, run } from './harness.mjs';
import {
  sanitizeRaw,
  isValidRaw,
  formatDisplay,
  buildPositionMap,
  applyEdit,
  normalizeRaw,
} from '../dist/amount.mjs';

// ---------- sanitizeRaw ----------

test('sanitizeRaw 只保留数字、首个小数点与首位负号', () => {
  assert.equal(sanitizeRaw('abc'), '');
  assert.equal(sanitizeRaw('1a2b3'), '123');
  assert.equal(sanitizeRaw('1.2.3'), '1.23');
  assert.equal(sanitizeRaw('--1--2'), '-12');
  assert.equal(sanitizeRaw('1-2'), '12');
  assert.equal(sanitizeRaw('abc-123'), '-123');
  assert.equal(sanitizeRaw('$1,234.56 USD'), '1234.56');
  assert.equal(sanitizeRaw('-'), '-');
  assert.equal(sanitizeRaw('.'), '.');
  assert.equal(sanitizeRaw('-.5'), '-.5');
});

// ---------- isValidRaw ----------

test('isValidRaw 接受中间态（负号、尾点、空前导）', () => {
  for (const ok of ['', '-', '0', '123', '-123', '123.', '-123.', '1.5', '.5', '-.5']) {
    assert.ok(isValidRaw(ok), ok);
  }
  for (const bad of ['--1', '1-', '1.2.3', '1,2', 'a', '-.5.']) {
    assert.ok(!isValidRaw(bad), bad);
  }
});

// ---------- formatDisplay ----------

test('formatDisplay 整数部分插入千位分隔符，其余原样保留', () => {
  assert.equal(formatDisplay(''), '');
  assert.equal(formatDisplay('-'), '-');
  assert.equal(formatDisplay('0'), '0');
  assert.equal(formatDisplay('123'), '123');
  assert.equal(formatDisplay('1234'), '1,234');
  assert.equal(formatDisplay('1234567'), '1,234,567');
  assert.equal(formatDisplay('-1234567'), '-1,234,567');
  assert.equal(formatDisplay('1234567.89'), '1,234,567.89');
  assert.equal(formatDisplay('123.'), '123.');
  assert.equal(formatDisplay('1234.'), '1,234.');
  assert.equal(formatDisplay('.5'), '.5');
  assert.equal(formatDisplay('-.5'), '-.5');
});

// ---------- buildPositionMap ----------

test('位置映射双向一致：displayToRaw[rawToDisplay[r]] === r', () => {
  const raws = ['', '-', '1', '1234567', '-1234567.89', '1234.', '.5', '1000000'];
  for (const raw of raws) {
    const display = formatDisplay(raw);
    const map = buildPositionMap(display);
    assert.equal(map.rawToDisplay.length, raw.length + 1);
    assert.equal(map.displayToRaw.length, display.length + 1);
    for (let r = 0; r <= raw.length; r++) {
      assert.equal(map.displayToRaw[map.rawToDisplay[r]], r, `${raw} @${r}`);
    }
    // 光标永不落在逗号之后（紧贴下一个逻辑字符）
    for (let r = 0; r <= raw.length; r++) {
      const d = map.rawToDisplay[r];
      if (d < display.length) assert.notEqual(display[d], ',');
    }
  }
});

// ---------- applyEdit：输入 ----------

test('输入触发重新分组并正确映射光标', () => {
  // '123' 末尾输入 '4'
  let r = applyEdit('123', 3, 3, '4');
  assert.deepEqual(r, { raw: '1234', display: '1,234', cursor: 5 });

  // '1,234' 在 '1' 之后（display 偏移 1）输入 '5' → '15,234'
  r = applyEdit('1234', 1, 1, '5');
  assert.deepEqual(r, { raw: '15234', display: '15,234', cursor: 3 });

  // 跨过逗号的选区中输入：'1,234' 选 [1,4)（',23'）输入 '9' → '194' → wait
  r = applyEdit('1234', 1, 4, '9');
  assert.deepEqual(r, { raw: '194', display: '194', cursor: 2 });
});

test('负号只能在首位', () => {
  // 空串首位输入 '-'
  let r = applyEdit('', 0, 0, '-');
  assert.deepEqual(r, { raw: '-', display: '-', cursor: 1 });

  // '123' 首位输入 '-'
  r = applyEdit('123', 0, 0, '-');
  assert.deepEqual(r, { raw: '-123', display: '-123', cursor: 1 });

  // 中间输入 '-' 被丢弃，光标停在原位
  r = applyEdit('123', 2, 2, '-');
  assert.deepEqual(r, { raw: '123', display: '123', cursor: 2 });

  // 已有负号时再输入 '-' 被丢弃
  r = applyEdit('-123', 0, 0, '-');
  assert.deepEqual(r, { raw: '-123', display: '-123', cursor: 1 });

  // 全选后输入 '-' 可以替换
  r = applyEdit('123', 0, 3, '-');
  assert.deepEqual(r, { raw: '-', display: '-', cursor: 1 });
});

test('小数点只能有一个，尾点中间态保留', () => {
  let r = applyEdit('123', 3, 3, '.');
  assert.deepEqual(r, { raw: '123.', display: '123.', cursor: 4 });

  r = applyEdit('1.2', 3, 3, '.');
  assert.deepEqual(r, { raw: '1.2', display: '1.2', cursor: 3 });

  r = applyEdit('123', 0, 0, '.');
  assert.deepEqual(r, { raw: '.123', display: '.123', cursor: 1 });
});

// ---------- applyEdit：删除 ----------

test('跨逗号选区删除', () => {
  // '1,234' 删 [0,3)（'1,2'）→ '34'
  const r = applyEdit('1234', 0, 3, '');
  assert.deepEqual(r, { raw: '34', display: '34', cursor: 0 });
});

test('Backspace 落在逗号上时连同前一位数字删除', () => {
  // '1,234' 光标在 2（逗号与 '2' 之间），Backspace 删除 [1,2)（仅逗号）
  const r = applyEdit('1234', 1, 2, '', { deleteDirection: 'backward' });
  assert.deepEqual(r, { raw: '234', display: '234', cursor: 0 });
});

test('Delete 落在逗号上时连同后一位数字删除', () => {
  // '1,234' 光标在 1（'1' 与逗号之间），Delete 删除 [1,2)（仅逗号）
  const r = applyEdit('1234', 1, 2, '', { deleteDirection: 'forward' });
  assert.deepEqual(r, { raw: '134', display: '134', cursor: 1 });
});

test('默认不扩展：仅选中逗号的删除是 no-op', () => {
  const r = applyEdit('1234', 1, 2, '');
  assert.deepEqual(r, { raw: '1234', display: '1,234', cursor: 2 });
});

// ---------- applyEdit：粘贴 ----------

test('粘贴含噪声文本被清洗并重新分组', () => {
  let r = applyEdit('', 0, 0, '$1,234.56 USD');
  assert.deepEqual(r, { raw: '1234.56', display: '1,234.56', cursor: 8 });

  r = applyEdit('', 0, 0, 'abc-12.3.4');
  assert.deepEqual(r, { raw: '-12.34', display: '-12.34', cursor: 6 });

  // 粘贴到选区上（替换）
  r = applyEdit('9999', 0, 5, '1,000'); // display 为 '9,999'，全选到 5
  assert.deepEqual(r, { raw: '1000', display: '1,000', cursor: 5 });
});

// ---------- normalizeRaw ----------

test('normalizeRaw 收尾中间态', () => {
  assert.equal(normalizeRaw(''), '');
  assert.equal(normalizeRaw('-'), '');
  assert.equal(normalizeRaw('123.'), '123');
  assert.equal(normalizeRaw('.5'), '0.5');
  assert.equal(normalizeRaw('-.5'), '-0.5');
  assert.equal(normalizeRaw('007'), '7');
  assert.equal(normalizeRaw('00.50'), '0.50');
  assert.equal(normalizeRaw('-0'), '-0');
  assert.equal(normalizeRaw('1234.5'), '1234.5');
});

run();
