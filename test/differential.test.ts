/**
 * 差分测试:随机文档 + 随机多层批注(含同点零宽)+ 随机操作组合
 * (单编辑 / 批量编辑 / 批量替换 / 增删批注),每步之后用逐字符参考模型
 * 逐位置核对区间库的覆盖查询、失效记录与分段输出。
 */
import { expect, test } from 'bun:test';
import { AnnotationStore, type Affinity, type Edit } from '../src/annotation-store';
import { ReferenceModel } from './reference-model';

type Rng = () => number;

function mulberry32(seed: number): Rng {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randInt(rng: Rng, lo: number, hi: number): number {
  return lo + Math.floor(rng() * (hi - lo + 1));
}

const ALPHABET = 'ab';

function randomText(rng: Rng, maxLen: number): string {
  const n = randInt(rng, 0, maxLen);
  let s = '';
  for (let i = 0; i < n; i++) s += ALPHABET[randInt(rng, 0, ALPHABET.length - 1)];
  return s;
}

function randomAffinity(rng: Rng): Affinity {
  return rng() < 0.5 ? 'left' : 'right';
}

function randomEdit(rng: Rng, len: number): Edit {
  const start = randInt(rng, 0, len);
  const deleteCount = randInt(rng, 0, Math.min(len - start, 5));
  return { start, deleteCount, text: randomText(rng, 4) };
}

function batchDisjoint(edits: Edit[]): boolean {
  for (let i = 1; i < edits.length; i++) {
    const prev = edits[i - 1]!;
    const cur = edits[i]!;
    if (prev.start + prev.deleteCount > cur.start) return false;
    if (prev.deleteCount === 0 && cur.deleteCount === 0 && prev.start === cur.start) return false;
  }
  return true;
}

function randomBatch(rng: Rng, len: number): Edit[] {
  for (let attempt = 0; attempt < 30; attempt++) {
    const n = randInt(rng, 2, 3);
    const edits = Array.from({ length: n }, () => randomEdit(rng, len)).sort(
      (a, b) => a.start - b.start,
    );
    if (batchDisjoint(edits)) return edits;
  }
  return [randomEdit(rng, len)];
}

function compareAll(store: AnnotationStore, ref: ReferenceModel): void {
  expect(store.getText()).toBe(ref.text);
  // 全量状态(区间、零宽、失效记录)核对。
  const state = store
    .annotations()
    .map((a) => ({ id: a.id, start: a.start, end: a.end, invalid: a.invalid }));
  expect(state).toEqual(ref.state());
  // 逐位置覆盖核对。
  for (let pos = 0; pos <= store.length; pos++) {
    expect(store.annotationsAt(pos).map((a) => a.id)).toEqual(ref.coveredAt(pos));
  }
  // 分段输出核对。
  expect(store.segments()).toEqual(ref.segments());
}

test('随机操作序列与逐字符参考模型逐步一致', () => {
  for (let seed = 1; seed <= 250; seed++) {
    const rng = mulberry32(seed);
    const text = randomText(rng, 12);
    const store = new AnnotationStore(text);
    const ref = new ReferenceModel(text);
    let nextId = 0;

    const addRandom = () => {
      const len = store.length;
      const start = randInt(rng, 0, len);
      // 30% 概率零宽批注;小文档上同点零宽会自然频繁出现。
      const zeroWidth = rng() < 0.3;
      const end = zeroWidth ? start : randInt(rng, start, Math.min(len, start + 8));
      const sa = randomAffinity(rng);
      const ea = randomAffinity(rng);
      const id = `ann${String(nextId++).padStart(3, '0')}`;
      store.add({ id, start, end, startAffinity: sa, endAffinity: ea });
      ref.add(id, start, end, sa, ea);
    };

    for (let i = 0; i < 5; i++) addRandom();
    compareAll(store, ref);

    for (let step = 0; step < 60; step++) {
      const r = rng();
      if (r < 0.45) {
        const edit = randomEdit(rng, store.length);
        store.applyEdit(edit);
        ref.applyEdit(edit.start, edit.deleteCount, edit.text ?? '');
      } else if (r < 0.7) {
        const edits = randomBatch(rng, store.length);
        store.applyEdits(edits);
        ref.applyEdits(edits);
      } else if (r < 0.85 && store.length > 0) {
        const i = randInt(rng, 0, store.length - 1);
        const j = randInt(rng, i + 1, Math.min(store.length, i + 3));
        const search = store.getText().slice(i, j);
        const replacement = randomText(rng, 3);
        expect(store.replaceAll(search, replacement)).toBe(ref.replaceAll(search, replacement));
      } else if (r < 0.95) {
        addRandom();
      } else {
        const all = store.annotations();
        if (all.length > 0) {
          const victim = all[randInt(rng, 0, all.length - 1)]!.id;
          store.remove(victim);
          ref.remove(victim);
        }
      }
      compareAll(store, ref);
    }
  }
});

test('同点零宽批注密集场景差分核对', () => {
  for (let seed = 1000; seed < 1050; seed++) {
    const rng = mulberry32(seed);
    const store = new AnnotationStore('ab');
    const ref = new ReferenceModel('ab');
    // 在同一间隙上堆叠多个不同黏附位的零宽批注。
    for (let i = 0; i < 6; i++) {
      const id = `p${i}`;
      const sa = randomAffinity(rng);
      const ea = randomAffinity(rng);
      store.add({ id, start: 1, end: 1, startAffinity: sa, endAffinity: ea });
      ref.add(id, 1, 1, sa, ea);
    }
    for (let step = 0; step < 30; step++) {
      const edit = randomEdit(rng, store.length);
      store.applyEdit(edit);
      ref.applyEdit(edit.start, edit.deleteCount, edit.text ?? '');
      compareAll(store, ref);
    }
  }
});
