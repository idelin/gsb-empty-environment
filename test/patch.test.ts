import {
  Assoc,
  apply,
  compose,
  fromEdits,
  invert,
  normalize,
  toEdits,
  transformPosition,
} from '../src/patch';
import { assert, assertEqual, test } from './framework';
import { mulberry32, randomDoc, randomEdits } from './random';

test('apply: 插入 / 删除 / 替换', () => {
  assertEqual(apply('abcdef', fromEdits(6, [{ start: 3, end: 3, text: 'XY' }])), 'abcXYdef');
  assertEqual(apply('abcdef', fromEdits(6, [{ start: 2, end: 4, text: '' }])), 'abef');
  assertEqual(apply('abcdef', fromEdits(6, [{ start: 2, end: 4, text: 'XYZ' }])), 'abXYZef');
  assertEqual(apply('', fromEdits(0, [])), '');
});

test('fromEdits: 乱序编辑自动排序，同补丁内重叠报错', () => {
  const p = fromEdits(10, [
    { start: 8, end: 9, text: 'x' },
    { start: 1, end: 3, text: 'yy' },
  ]);
  assertEqual(apply('0123456789', p), '0yy34567x9');
  let threw = false;
  try {
    fromEdits(10, [
      { start: 2, end: 6, text: 'a' },
      { start: 4, end: 8, text: 'b' },
    ]);
  } catch (e) {
    threw = true;
  }
  assert(threw, 'overlapping edits in one patch must throw');
});

test('fromEdits: 同位置多个插入按给定顺序拼接', () => {
  const p = fromEdits(6, [
    { start: 3, end: 3, text: 'XY' },
    { start: 3, end: 3, text: 'Q' },
  ]);
  assertEqual(apply('abcdef', p), 'abcXYQdef');
});

test('transformPosition: 插入点处的左右亲和性', () => {
  const p = fromEdits(6, [{ start: 3, end: 3, text: 'XY' }]);
  assertEqual(transformPosition(3, p, 'left'), 3);
  assertEqual(transformPosition(3, p, 'right'), 5);
  assertEqual(transformPosition(0, p, 'right'), 0);
  assertEqual(transformPosition(6, p, 'left'), 8);
});

test('transformPosition: 删除覆盖由亲和性决定（caret 依附相邻字符）', () => {
  const p = fromEdits(6, [{ start: 2, end: 4, text: '' }]); // 删除 'cd'
  // left 依附左侧字符：pos 2 依附 'b'（存活），pos 4 依附 'c'（被删）
  assertEqual(transformPosition(2, p, 'left'), 2);
  assertEqual(transformPosition(4, p, 'left'), null);
  // right 依附右侧字符：pos 2 依附 'c'（被删），pos 4 依附 'e'（存活）
  assertEqual(transformPosition(2, p, 'right'), null);
  assertEqual(transformPosition(4, p, 'right'), 2);
  // 严格内部两种亲和性都被覆盖
  assertEqual(transformPosition(3, p, 'left'), null);
  assertEqual(transformPosition(3, p, 'right'), null);
  assertEqual(transformPosition(5, p, 'right'), 3);
});

test('normalize: 同一断点处规范化为插入在删除之前', () => {
  const p = normalize([
    { type: 'delete', count: 2 },
    { type: 'insert', text: 'AB' },
  ]);
  assertEqual(p, [
    { type: 'insert', text: 'AB' },
    { type: 'delete', count: 2 },
  ]);
  assertEqual(apply('cd', p), 'AB');
});

test('invert: 删除与替换均可撤销', () => {
  const doc = 'alpha beta gamma';
  const del = fromEdits(doc.length, [{ start: 6, end: 11, text: '' }]);
  assertEqual(apply(apply(doc, del), invert(doc, del)), doc);
  const rep = fromEdits(doc.length, [{ start: 6, end: 10, text: 'BETA!' }]);
  assertEqual(apply(apply(doc, rep), invert(doc, rep)), doc);
});

test('compose: 连续补丁的重叠替换合并为单个编辑', () => {
  const doc = '0123456789';
  const p1 = fromEdits(doc.length, [{ start: 2, end: 8, text: 'abcdef' }]);
  const mid = apply(doc, p1);
  const p2 = fromEdits(mid.length, [{ start: 3, end: 5, text: 'XY' }]);
  const composed = compose(p1, p2);
  assertEqual(apply(doc, composed), apply(mid, p2));
  assertEqual(toEdits(composed), [{ start: 2, end: 8, text: 'aXYdef' }]);
});

test('随机不变量: compose/apply/invert/位置映射的组合律', () => {
  const rnd = mulberry32(20261005);
  const assocs: Assoc[] = ['left', 'right'];
  for (let iter = 0; iter < 400; iter++) {
    const doc = randomDoc(rnd);
    const pa = fromEdits(doc.length, randomEdits(doc.length, rnd));
    const mid = apply(doc, pa);
    const pb = fromEdits(mid.length, randomEdits(mid.length, rnd));
    const fin = apply(mid, pb);
    const pc = compose(pa, pb);

    assertEqual(apply(doc, pc), fin, 'compose/apply iter=' + iter);
    assertEqual(apply(mid, invert(doc, pa)), doc, 'invert iter=' + iter);

    for (let pos = 0; pos <= doc.length; pos++) {
      for (const assoc of assocs) {
        const step1 = transformPosition(pos, pa, assoc);
        const step = step1 === null ? null : transformPosition(step1, pb, assoc);
        const combined = transformPosition(pos, pc, assoc);
        assertEqual(
          combined,
          step,
          'position iter=' + iter + ' pos=' + pos + ' assoc=' + assoc +
            ' doc=' + JSON.stringify(doc)
        );
      }
    }
  }
});
