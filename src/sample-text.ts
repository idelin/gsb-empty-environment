const SENTENCES = [
  'the quick brown fox jumps over the lazy dog.',
  'pack my box with five dozen liquor jugs.',
  'how vexingly quick daft zebras jump.',
  'sphinx of black quartz, judge my vow.',
  'the five boxing wizards jump quickly.',
  'a reliable channel is a luxury we cannot afford.',
  'error containment beats error correction when bandwidth is cheap.',
  'sync words turn a catastrophic failure into a local one.',
];

/** 生成确定性的英文样例文本（ASCII 小写 + 空格 + 标点），长度约 targetLength。 */
export function buildSampleText(targetLength: number): string {
  let out = '';
  let i = 0;
  while (out.length < targetLength) {
    out += SENTENCES[i % SENTENCES.length] + ' ';
    i += 1;
  }
  return out.slice(0, targetLength);
}
