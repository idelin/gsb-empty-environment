import assert from 'node:assert/strict';
import { test, run } from './harness.mjs';
import { formatDisplay, isValidRaw, applyEdit } from '../dist/amount.mjs';

// 可复现的伪随机数（mulberry32）
function rng(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// display 偏移 → raw 偏移：数偏移之前的非逗号字符（朴素参考实现）
function displayOffsetToRawRef(display, offset) {
  let n = 0;
  for (let i = 0; i < offset && i < display.length; i++) {
    if (display[i] !== ',') n++;
  }
  return n;
}

// 参考清洗：直接过滤带标记的 token 序列，标记随 token 存活
function refSanitizeTokens(tokens, cursor) {
  const out = [];
  let seenDot = false;
  let kept = 0;
  for (let i = 0; i < tokens.length; i++) {
    const ch = tokens[i].ch;
    let keep = false;
    if (ch >= '0' && ch <= '9') keep = true;
    else if (ch === '.' && !seenDot) {
      seenDot = true;
      keep = true;
    } else if (ch === '-' && out.length === 0) keep = true;
    if (keep) out.push(tokens[i]);
    if (i < cursor && keep) kept++;
  }
  return { tokens: out, cursor: kept };
}

const rawOf = (tokens) => tokens.map((t) => t.ch).join('');

const INSERT_ALPHABET = '00123456789...---,,abc $';
const DIRECTIONS = ['none', 'backward', 'forward'];

test('连续编辑模拟：用原始字符标记核对 raw 与光标', () => {
  for (let seed = 1; seed <= 8; seed++) {
    const rand = rng(seed);
    let nextId = 1;
    let tokens = []; // 模型：每个原始字符携带唯一标记 id
    let prevIds = []; // 上一步的标记序列
    let cursor = 0; // display 坐标下的光标（折叠选区）

    for (let step = 0; step < 400; step++) {
      const raw = rawOf(tokens);
      const display = formatDisplay(raw);

      // 随机选区（半数折叠为光标）
      let selStart = Math.floor(rand() * (display.length + 1));
      let selEnd = Math.floor(rand() * (display.length + 1));
      if (rand() < 0.5) selEnd = selStart;
      if (selEnd < selStart) [selStart, selEnd] = [selEnd, selStart];

      // 随机操作：插入 / 粘贴（含噪声）或删除
      let inserted = '';
      if (rand() < 0.6) {
        const len = 1 + Math.floor(rand() * 6);
        let s = '';
        for (let k = 0; k < len; k++) {
          s += INSERT_ALPHABET[Math.floor(rand() * INSERT_ALPHABET.length)];
        }
        inserted = s;
      }
      const deleteDirection = DIRECTIONS[Math.floor(rand() * DIRECTIONS.length)];

      // ---- 被测库 ----
      const result = applyEdit(raw, selStart, selEnd, inserted, { deleteDirection });

      // ---- 标记模型（独立参考）----
      let rs = displayOffsetToRawRef(display, selStart);
      let re = displayOffsetToRawRef(display, selEnd);
      if (inserted === '' && selStart !== selEnd && rs === re) {
        if (deleteDirection === 'backward' && rs > 0) rs--;
        else if (deleteDirection === 'forward' && re < tokens.length) re++;
      }
      const fragment = inserted.replace(/[^0-9.\-]/g, '');
      const fragTokens = [...fragment].map((ch) => ({ ch, id: nextId++ }));
      const spliced = [...tokens.slice(0, rs), ...fragTokens, ...tokens.slice(re)];
      const model = refSanitizeTokens(spliced, rs + fragTokens.length);

      // ---- 核对 1：raw 完全一致（存活标记的字符序列一致）----
      assert.equal(
        result.raw,
        rawOf(model.tokens),
        `seed=${seed} step=${step} raw=${JSON.stringify(raw)} sel=[${selStart},${selEnd}) ins=${JSON.stringify(inserted)}`
      );

      // ---- 核对 2：光标映射回 raw 偏移后与模型一致 ----
      const resultRawCursor = displayOffsetToRawRef(result.display, result.cursor);
      assert.equal(
        resultRawCursor,
        model.cursor,
        `seed=${seed} step=${step} cursor raw=${JSON.stringify(result.raw)}`
      );

      // ---- 核对 3：不变量 ----
      assert.ok(isValidRaw(result.raw), 'raw 始终合法');
      assert.equal(result.display, formatDisplay(result.raw), 'display 与 raw 一致');
      assert.equal(result.display.replace(/,/g, ''), result.raw, '去掉逗号即 raw');
      if (result.cursor < result.display.length) {
        assert.notEqual(result.display[result.cursor], ',', '光标不停在逗号前');
      }

      // ---- 核对 4：标记核对 ----
      const ids = model.tokens.map((t) => t.id);
      const boundary = nextId - fragTokens.length; // 本步新生标记的下界
      // 4a. 标记不重复
      assert.equal(new Set(ids).size, ids.length, '标记不重复');
      // 4b. 存活老标记是上一步标记序列的子序列（相对顺序保持、无复制）
      const oldIds = ids.filter((id) => id < boundary);
      let pi = 0;
      for (const id of oldIds) {
        while (pi < prevIds.length && prevIds[pi] !== id) pi++;
        assert.ok(pi < prevIds.length, `老标记 ${id} 保持原有相对顺序`);
        pi++;
      }
      // 4c. 新生标记的幸存者在序列中保持连续（不被老标记穿插）
      const bornIdx = ids.map((id, i) => (id >= boundary ? i : -1)).filter((i) => i >= 0);
      for (let i = 1; i < bornIdx.length; i++) {
        assert.equal(bornIdx[i], bornIdx[i - 1] + 1, '新生标记连续');
      }

      prevIds = ids;
      tokens = model.tokens;
      cursor = result.cursor;
    }
  }
});

run();
