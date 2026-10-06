// 测试基础设施：极简断言、确定性随机数、独立扁平字符-标记参考模型。
// 参考模型与 src/ 实现相互独立，用于交叉核对。

const registry = [];

export function test(name, fn) {
  registry.push({ name, fn });
}

export function getTests() {
  return registry;
}

export function assert(condition, message) {
  if (!condition) throw new Error('assertion failed: ' + message);
}

export function deepEqual(a, b) {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keysA = Object.keys(a).filter((k) => a[k] !== undefined);
  const keysB = Object.keys(b).filter((k) => b[k] !== undefined);
  if (keysA.length !== keysB.length) return false;
  for (const key of keysA) {
    if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
    if (!deepEqual(a[key], b[key])) return false;
  }
  return true;
}

export function assertDeepEqual(actual, expected, message) {
  if (!deepEqual(actual, expected)) {
    throw new Error(
      message + '\n  actual:   ' + JSON.stringify(actual) + '\n  expected: ' + JSON.stringify(expected)
    );
  }
}

export function mulberry32(seed) {
  let state = seed >>> 0;
  return function () {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---- 独立参考模型：扁平字符-标记序列 ----

export function keyOf(mark) {
  let key = mark.type;
  if (mark.attrs) {
    for (const name of Object.keys(mark.attrs).sort()) {
      key += '|' + JSON.stringify(name) + '=' + JSON.stringify(mark.attrs[name]);
    }
  }
  return key;
}

export function typeOfKey(key) {
  return key.split('|')[0];
}

export function flatOf(doc) {
  const out = [];
  for (const node of doc.nodes) {
    const keys = [];
    const seen = new Set();
    for (const mark of node.marks) {
      const key = keyOf(mark);
      if (seen.has(key)) continue;
      if (mark.type === 'link' && keys.some((k) => typeOfKey(k) === 'link')) continue;
      seen.add(key);
      keys.push(key);
    }
    keys.sort();
    for (const char of node.text) out.push({ char, marks: [...keys] });
  }
  return out;
}

export function flatApplyEdits(flat, edits) {
  const out = flat.map((cell) => ({ char: cell.char, marks: [...cell.marks] }));
  const order = edits
    .map((edit, index) => index)
    .sort((a, b) => edits[b].start - edits[a].start || edits[b].end - edits[a].end);
  for (const index of order) {
    const edit = edits[index];
    const key = keyOf(edit.mark);
    for (let pos = edit.start; pos < edit.end; pos++) {
      const cell = out[pos];
      const marks = cell.marks.filter((k) => typeOfKey(k) !== edit.mark.type);
      if (edit.kind === 'add') marks.push(key);
      marks.sort();
      cell.marks = marks;
    }
  }
  return out;
}

// ---- 随机文档 / 编辑生成 ----

const PLAIN_TYPES = ['bold', 'italic', 'underline', 'strike', 'code'];

export function randomMark(rng) {
  if (rng() < 0.25) {
    return { type: 'link', attrs: { href: rng() < 0.5 ? 'https://a' : 'https://b' } };
  }
  return { type: PLAIN_TYPES[Math.floor(rng() * PLAIN_TYPES.length)] };
}

export function randomDoc(rng, idPrefix) {
  const count = 1 + Math.floor(rng() * 6);
  const nodes = [];
  for (let i = 0; i < count; i++) {
    const length = Math.floor(rng() * 5);
    let text = '';
    for (let j = 0; j < length; j++) text += rng() < 0.5 ? 'a' : 'b';
    const marks = [];
    const markCount = Math.floor(rng() * 3);
    for (let j = 0; j < markCount; j++) marks.push(randomMark(rng));
    const node = { id: idPrefix + '_' + i, text, marks };
    if (rng() < 0.3) {
      node.anchor = { id: 'anchor_' + idPrefix + '_' + i, affinity: rng() < 0.5 ? 'left' : 'right' };
    }
    nodes.push(node);
  }
  return { nodes };
}

export function randomEdits(rng, length, count) {
  const edits = [];
  for (let i = 0; i < count; i++) {
    const a = Math.floor(rng() * (length + 1));
    const b = Math.floor(rng() * (length + 1));
    edits.push({
      kind: rng() < 0.5 ? 'add' : 'remove',
      start: Math.min(a, b),
      end: Math.max(a, b),
      mark: randomMark(rng),
    });
  }
  return edits;
}

export function anchorsOf(doc) {
  return doc.nodes
    .filter((node) => node.anchor)
    .map((node) => node.anchor.id + ':' + node.anchor.affinity)
    .sort();
}

export function emptyAnchoredCount(doc) {
  return doc.nodes.filter((node) => node.anchor && node.text.length === 0).length;
}
