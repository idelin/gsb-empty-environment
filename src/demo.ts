/**
 * 运行样例：
 *  1) 单节点开头/结尾 1000 次密集交替插入，观察标识符深度增长曲线；
 *  2) 多节点并发编辑 + 随机全排列合并，验证收敛与因果序。
 */
import { LseqDocument, type Op, mulberry32 } from '../src/document.ts';

function part(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// ---------- 样例 1：密集交替插入，深度增长平缓 ----------
part('样例 1：开头/结尾 1000 次密集交替插入');
{
  const doc = new LseqDocument(1, mulberry32(2024));
  const N = 1000;
  for (let i = 0; i < N; i++) {
    const ch = String.fromCharCode(0x61 + (i % 26));
    if (i % 2 === 0) doc.insert(0, ch);
    else doc.insert(doc.length, ch);
    if ((i + 1) % 100 === 0) {
      const s = doc.stats();
      console.log(
        `  插入 ${String(i + 1).padStart(4)} 次 -> ` +
          `maxDepth=${s.maxDepth} avgDepth=${s.avgDepth.toFixed(2)} maxBits=${s.maxBits}`,
      );
    }
  }
  const s = doc.stats();
  console.log(`  最终: 文本长度=${doc.length} maxDepth=${s.maxDepth} maxBits=${s.maxBits}`);
  console.log(`  文本预览(前40字符): ${doc.text().slice(0, 40)}`);
  console.log('  部分标识符样例:');
  for (const line of doc.debugIds().slice(0, 3)) console.log(`    ${line}`);
}

// ---------- 样例 2：多节点随机全排列合并 ----------
part('样例 2：多节点并发编辑 + 随机全排列合并');
{
  const SITES = 4;
  const seed = 777;
  const sources: LseqDocument[] = [];
  const pool: Op[] = [];
  for (let s = 0; s < SITES; s++) sources.push(new LseqDocument(s + 1, mulberry32(seed + s)));

  const random = mulberry32(seed);
  for (let round = 0; round < 20; round++) {
    for (let s = 0; s < SITES; s++) {
      const doc = sources[s];
      const ops: Op[] = [];
      ops.push(doc.insert(Math.floor(random() * (doc.length + 1)), `[${s},${round}]`));
      if (doc.length > 6 && random() < 0.3) {
        ops.push(doc.delete(Math.floor(random() * doc.length)));
      }
      for (let t = 0; t < SITES; t++) {
        if (t !== s) for (const op of ops) sources[t].apply(op);
      }
      pool.push(...ops);
    }
  }
  const reference = sources[0].text();
  console.log(`  ${SITES} 个节点共产生 ${pool.length} 个操作，因果广播后文本长度=${reference.length}`);

  function shuffle<T>(arr: T[], r: () => number): T[] {
    const a = arr.slice();
    for (let i = a.length - 1; i > 0; i--) {
      const j = Math.floor(r() * (i + 1));
      [a[i], a[j]] = [a[j], a[i]];
    }
    return a;
  }

  let allOk = true;
  for (let trial = 0; trial < 5; trial++) {
    const r = mulberry32(seed * 31 + trial);
    const replica = new LseqDocument(100 + trial, r);
    replica.merge(shuffle(pool, r));
    const ok = replica.text() === reference;
    allOk &&= ok;
    console.log(`  随机排列合并 trial=${trial}: ${ok ? '收敛一致 ✓' : '不一致 ✗'}`);
  }
  console.log(`  结论: ${allOk ? '所有随机排列均收敛到严格因果序 ✓' : '存在分歧 ✗'}`);
  console.log(`  最终文本预览: ${reference.slice(0, 60)}...`);
}
