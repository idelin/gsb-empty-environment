import { Edit } from '../src/patch';

/** 确定性伪随机数生成器（mulberry32），保证测试可复现。 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const WORDS = ['foo', 'bar', 'foo bar', 'baz', 'foo', 'ab', ''];

/** 生成带大量重复片段的随机文档，制造上下文歧义。 */
export function randomDoc(rnd: () => number): string {
  const parts: string[] = [];
  const n = Math.floor(rnd() * 8);
  for (let i = 0; i < n; i++) {
    parts.push(WORDS[Math.floor(rnd() * WORDS.length)]);
    if (rnd() < 0.5) parts.push(' ');
  }
  return parts.join('');
}

export function randomText(rnd: () => number): string {
  const alphabet = 'ab ';
  const n = Math.floor(rnd() * 5);
  let s = '';
  for (let i = 0; i < n; i++) s += alphabet[Math.floor(rnd() * alphabet.length)];
  return s;
}

export function randomEdits(docLen: number, rnd: () => number): Edit[] {
  const edits: Edit[] = [];
  const n = Math.floor(rnd() * 3);
  let cursor = 0;
  for (let i = 0; i < n; i++) {
    if (docLen - cursor < 0) break;
    const start = cursor + Math.floor(rnd() * (docLen - cursor + 1));
    const end = start + Math.floor(rnd() * (docLen - start + 1));
    edits.push({ start, end, text: randomText(rnd) });
    cursor = end;
  }
  return edits;
}
