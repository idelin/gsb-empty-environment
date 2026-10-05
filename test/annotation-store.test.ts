import { describe, expect, test } from 'bun:test';
import { AnnotationStore, mapPositionThroughEdit } from '../src/annotation-store';

describe('多层叠加与分段输出', () => {
  test('每段活跃批注集合按 ID 排序', () => {
    const store = new AnnotationStore('abcdefghij');
    store.add({ id: 'c', start: 0, end: 4 });
    store.add({ id: 'a', start: 2, end: 6 });
    store.add({ id: 'b', start: 4, end: 10 });
    expect(store.segments()).toEqual([
      { start: 0, end: 2, ids: ['c'] },
      { start: 2, end: 4, ids: ['a', 'c'] },
      { start: 4, end: 6, ids: ['a', 'b'] },
      { start: 6, end: 10, ids: ['b'] },
    ]);
    expect(store.annotationsAt(3).map((a) => a.id)).toEqual(['a', 'c']);
    expect(store.annotationsInRange(1, 5).map((a) => a.id)).toEqual(['a', 'b', 'c']);
    expect(store.annotationsInRange(6, 10).map((a) => a.id)).toEqual(['b']);
  });

  test('零宽批注归入所在段,文档末尾零宽批注归入最后一段', () => {
    const store = new AnnotationStore('ab');
    store.add({ id: 'p', start: 2, end: 2 });
    expect(store.segments()).toEqual([{ start: 0, end: 2, ids: ['p'] }]);
    const empty = new AnnotationStore('');
    empty.add({ id: 'p', start: 0, end: 0 });
    expect(empty.segments()).toEqual([{ start: 0, end: 0, ids: ['p'] }]);
  });
});

describe('边界插入由左右黏附位决定', () => {
  const cases: Array<[sa: 'left' | 'right', ea: 'left' | 'right']> = [
    ['left', 'right'],
    ['right', 'left'],
    ['left', 'left'],
    ['right', 'right'],
  ];
  // 文本 "abcdef",批注 [2,4) = "cd";在起点 2 或终点 4 插入 "XY"。
  const expected: Record<string, { atStart: [number, number]; atEnd: [number, number] }> = {
    'left,right': { atStart: [2, 6], atEnd: [2, 6] },
    'right,left': { atStart: [4, 6], atEnd: [2, 4] },
    'left,left': { atStart: [2, 6], atEnd: [2, 4] },
    'right,right': { atStart: [4, 6], atEnd: [2, 6] },
  };
  for (const [sa, ea] of cases) {
    test(`startAffinity=${sa}, endAffinity=${ea}`, () => {
      const key = `${sa},${ea}`;
      const s1 = new AnnotationStore('abcdef');
      s1.add({ id: 'x', start: 2, end: 4, startAffinity: sa, endAffinity: ea });
      s1.applyEdit({ start: 2, deleteCount: 0, text: 'XY' });
      const a1 = s1.get('x')!;
      expect([a1.start, a1.end]).toEqual(expected[key]!.atStart);

      const s2 = new AnnotationStore('abcdef');
      s2.add({ id: 'x', start: 2, end: 4, startAffinity: sa, endAffinity: ea });
      s2.applyEdit({ start: 4, deleteCount: 0, text: 'XY' });
      const a2 = s2.get('x')!;
      expect([a2.start, a2.end]).toEqual(expected[key]!.atEnd);
    });
  }

  test('默认黏附位两端均扩展', () => {
    const store = new AnnotationStore('abcdef');
    store.add({ id: 'x', start: 2, end: 4 });
    store.applyEdit({ start: 2, deleteCount: 0, text: 'XY' });
    store.applyEdit({ start: 6, deleteCount: 0, text: 'Z' });
    const a = store.get('x')!;
    expect([a.start, a.end]).toEqual([2, 7]);
    expect(store.getText().slice(2, 7)).toBe('XYcdZ');
  });
});

describe('删除整个批注与失效记录', () => {
  test('整体删除保留失效记录,恢复插入可重新定位', () => {
    const store = new AnnotationStore('hello world');
    store.add({ id: 'a', start: 6, end: 11 });
    store.applyEdit({ start: 6, deleteCount: 5 });
    expect(store.getText()).toBe('hello ');
    const tomb = store.get('a')!;
    expect(tomb.invalid).toBe(true);
    expect([tomb.start, tomb.end]).toEqual([6, 6]);
    expect(tomb.invalidatedFrom).toEqual({ start: 6, end: 11 });
    expect(store.tombstones().map((t) => t.id)).toEqual(['a']);
    expect(store.annotationsAt(6)).toEqual([]);
    expect(store.segments()).toEqual([{ start: 0, end: 6, ids: [] }]);

    // 撤销删除:在同一位置重新插入原文,批注重新定位。
    store.applyEdit({ start: 6, deleteCount: 0, text: 'world' });
    const revived = store.get('a')!;
    expect(revived.invalid).toBe(false);
    expect([revived.start, revived.end]).toEqual([6, 11]);
    expect(store.tombstones()).toEqual([]);
  });

  test('失效记录跟随后续编辑移动,可在新位置恢复', () => {
    const store = new AnnotationStore('hello world');
    store.add({ id: 'a', start: 6, end: 8 }); // "wo"
    store.applyEdit({ start: 6, deleteCount: 2 });
    expect(store.getText()).toBe('hello rld');
    store.applyEdit({ start: 0, deleteCount: 0, text: 'XYZ' });
    expect(store.tombstones()[0]).toMatchObject({ id: 'a', start: 9, invalid: true });
    store.applyEdit({ start: 9, deleteCount: 0, text: 'wo' });
    expect(store.getText()).toBe('XYZhello world');
    const a = store.get('a')!;
    expect(a.invalid).toBe(false);
    expect([a.start, a.end]).toEqual([9, 11]);
  });

  test('删除更大范围时批注坍缩为失效记录', () => {
    const store = new AnnotationStore('abcdef');
    store.add({ id: 'a', start: 1, end: 3 });
    store.applyEdit({ start: 0, deleteCount: 6 });
    const tomb = store.get('a')!;
    expect(tomb.invalid).toBe(true);
    expect([tomb.start, tomb.end]).toEqual([0, 0]);
    store.applyEdit({ start: 0, deleteCount: 0, text: 'xy' });
    const a = store.get('a')!;
    expect([a.start, a.end]).toEqual([0, 2]);
  });

  test('显式 remove 不留失效记录', () => {
    const store = new AnnotationStore('abc');
    store.add({ id: 'a', start: 0, end: 2 });
    expect(store.remove('a')).toBe(true);
    expect(store.get('a')).toBeUndefined();
    expect(store.tombstones()).toEqual([]);
  });
});

describe('同点零宽批注', () => {
  test('同一点上的多个零宽批注各自独立、按 ID 排序输出', () => {
    const store = new AnnotationStore('abc');
    store.add({ id: 'p2', start: 1, end: 1 });
    store.add({ id: 'p1', start: 1, end: 1 });
    store.add({ id: 'p3', start: 1, end: 1, startAffinity: 'right' });
    expect(store.annotationsAt(1).map((a) => a.id)).toEqual(['p1', 'p2', 'p3']);

    // 在同点插入:p1/p2(左黏附)留在插入文本之前,p3(右黏附)移到之后。
    store.applyEdit({ start: 1, deleteCount: 0, text: 'XY' });
    expect(store.annotationsAt(1).map((a) => a.id)).toEqual(['p1', 'p2']);
    expect(store.annotationsAt(3).map((a) => a.id)).toEqual(['p3']);
    expect(store.annotationsAt(2)).toEqual([]);
  });

  test('点批注映射通过删除与替换', () => {
    const s1 = new AnnotationStore('abcdef');
    s1.add({ id: 'p', start: 3, end: 3, startAffinity: 'left' });
    s1.applyEdit({ start: 1, deleteCount: 4 }); // 删除 "bcde"
    expect(s1.getText()).toBe('af');
    expect(s1.get('p')).toMatchObject({ start: 1, end: 1, invalid: false });

    const s2 = new AnnotationStore('abcdef');
    s2.add({ id: 'p', start: 1, end: 1, startAffinity: 'left' });
    s2.applyEdit({ start: 1, deleteCount: 4, text: 'XY' });
    expect(s2.get('p')).toMatchObject({ start: 1, end: 1 });

    const s3 = new AnnotationStore('abcdef');
    s3.add({ id: 'p', start: 1, end: 1, startAffinity: 'right' });
    s3.applyEdit({ start: 1, deleteCount: 4, text: 'XY' });
    expect(s3.get('p')).toMatchObject({ start: 3, end: 3 });
  });
});

describe('替换与批量替换', () => {
  test('整体替换把批注迁移到替换文本', () => {
    const store = new AnnotationStore('foo bar');
    store.add({ id: 'a', start: 0, end: 3 });
    store.applyEdit({ start: 0, deleteCount: 3, text: 'foobar' });
    expect(store.getText()).toBe('foobar bar');
    expect(store.get('a')).toMatchObject({ start: 0, end: 6, invalid: false });
  });

  test('replaceAll 批量替换映射所有层', () => {
    const store = new AnnotationStore('foo foo');
    store.add({ id: 'a', start: 0, end: 3 });
    store.add({ id: 'b', start: 4, end: 7 });
    store.add({ id: 'c', start: 0, end: 7 });
    expect(store.replaceAll('foo', 'f')).toBe(2);
    expect(store.getText()).toBe('f f');
    expect(store.get('a')).toMatchObject({ start: 0, end: 1 });
    expect(store.get('b')).toMatchObject({ start: 2, end: 3 });
    expect(store.get('c')).toMatchObject({ start: 0, end: 3 });
  });

  test('replaceAll 空模式抛错', () => {
    const store = new AnnotationStore('abc');
    expect(() => store.replaceAll('', 'x')).toThrow();
  });
});

describe('批量编辑', () => {
  test('编辑前坐标的不重叠批量编辑', () => {
    const store = new AnnotationStore('abcdef');
    store.add({ id: 'x', start: 0, end: 6 });
    store.applyEdits([
      { start: 1, deleteCount: 1, text: 'B' },
      { start: 4, deleteCount: 1, text: 'E' },
    ]);
    expect(store.getText()).toBe('aBcdEf');
    expect(store.get('x')).toMatchObject({ start: 0, end: 6 });
  });

  test('重叠编辑与同点双插入被拒绝', () => {
    const store = new AnnotationStore('abcdef');
    expect(() =>
      store.applyEdits([
        { start: 1, deleteCount: 3 },
        { start: 2, deleteCount: 1 },
      ]),
    ).toThrow('overlapping');
    expect(() =>
      store.applyEdits([
        { start: 2, deleteCount: 0, text: 'x' },
        { start: 2, deleteCount: 0, text: 'y' },
      ]),
    ).toThrow('ambiguous');
    // 失败的批量编辑不应改动文档。
    expect(store.getText()).toBe('abcdef');
  });
});

describe('mapPositionThroughEdit', () => {
  test('位置映射遵循黏附位', () => {
    const edit = { start: 2, deleteCount: 2, text: 'XYZ' };
    expect(mapPositionThroughEdit(0, 'left', edit)).toBe(0);
    expect(mapPositionThroughEdit(5, 'left', edit)).toBe(6);
    expect(mapPositionThroughEdit(2, 'left', edit)).toBe(2);
    expect(mapPositionThroughEdit(2, 'right', edit)).toBe(5);
    expect(mapPositionThroughEdit(4, 'left', edit)).toBe(2);
    expect(mapPositionThroughEdit(4, 'right', edit)).toBe(5);
  });
});
