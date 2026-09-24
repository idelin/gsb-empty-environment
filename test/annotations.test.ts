import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  AnnotationStore,
  offsetAtPosition,
  positionAtOffset,
  isUtf16Boundary,
  type Annotation,
} from '../src/index.ts';

function makeAnnotation(overrides: Partial<Annotation> = {}): Annotation {
  return {
    id: 'a1',
    start: 2,
    end: 4,
    startAffinity: 'inward',
    endAffinity: 'inward',
    ...overrides,
  };
}

describe('insertion at the left endpoint of [2,4)', () => {
  it('inward start affinity does NOT include inserted text', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation({ startAffinity: 'inward' }));
    store.applyEdits([{ id: 1, offset: 2, deleteCount: 0, insertText: 'XY' }]);
    assert.equal(store.getText(), 'abXYcdef');
    assert.deepEqual(store.getAnnotation('a1'), {
      id: 'a1',
      start: 4,
      end: 6,
      startAffinity: 'inward',
      endAffinity: 'inward',
    });
  });

  it('outward start affinity includes inserted text', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation({ startAffinity: 'outward' }));
    store.applyEdits([{ id: 1, offset: 2, deleteCount: 0, insertText: 'XY' }]);
    assert.equal(store.getText(), 'abXYcdef');
    const a = store.getAnnotation('a1')!;
    assert.equal(a.start, 2);
    assert.equal(a.end, 6);
    assert.equal(store.getText().slice(a.start, a.end), 'XYcd');
  });
});

describe('insertion at the right endpoint of [2,4)', () => {
  it('inward end affinity does NOT include inserted text', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation({ endAffinity: 'inward' }));
    store.applyEdits([{ id: 1, offset: 4, deleteCount: 0, insertText: 'XY' }]);
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [2, 4]);
  });

  it('outward end affinity includes inserted text', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation({ endAffinity: 'outward' }));
    store.applyEdits([{ id: 1, offset: 4, deleteCount: 0, insertText: 'XY' }]);
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [2, 6]);
    assert.equal(store.getText().slice(a.start, a.end), 'cdXY');
  });
});

describe('deletion', () => {
  it('deleting all covered content keeps a zero-width anchor', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation());
    store.applyEdits([{ id: 1, offset: 2, deleteCount: 2 }]);
    assert.equal(store.getText(), 'abef');
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [2, 2]);
  });

  it('the zero-width anchor keeps tracking later edits', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation());
    store.applyEdits([{ id: 1, offset: 2, deleteCount: 2 }]);
    store.applyEdits([{ id: 2, offset: 0, deleteCount: 0, insertText: '>>' }]);
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [4, 4]);
  });

  it('a delete covering the whole range plus surroundings never yields negative length', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation());
    store.applyEdits([{ id: 1, offset: 0, deleteCount: 6 }]);
    assert.equal(store.getText(), '');
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [0, 0]);
    assert.ok(a.end >= a.start);
  });

  it('partial overlap shrinks the range', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation({ start: 1, end: 5 }));
    store.applyEdits([{ id: 1, offset: 3, deleteCount: 2 }]); // delete "de"
    assert.equal(store.getText(), 'abcf');
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [1, 3]);
    assert.equal(store.getText().slice(a.start, a.end), 'bc');
  });

  it('replace (delete + insert) covering the range collapses to an anchor at the edit point', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation());
    store.applyEdits([{ id: 1, offset: 2, deleteCount: 2, insertText: 'Z' }]);
    assert.equal(store.getText(), 'abZef');
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [2, 2]);
  });
});

describe('batched edits', () => {
  it('edits at the same offset apply in ascending operation-ID order', () => {
    const store = new AnnotationStore('ac');
    store.applyEdits([
      { id: 20, offset: 1, deleteCount: 0, insertText: '2' },
      { id: 10, offset: 1, deleteCount: 0, insertText: '1' },
      { id: 30, offset: 1, deleteCount: 0, insertText: '3' },
    ]);
    assert.equal(store.getText(), 'a123c');
  });

  it('a batch applies by offset then ID and is one undo step', () => {
    const store = new AnnotationStore('hello world');
    store.addAnnotation(makeAnnotation({ id: 'w', start: 6, end: 11 }));
    store.applyEdits([
      { id: 2, offset: 11, deleteCount: 0, insertText: '!' },
      { id: 1, offset: 0, deleteCount: 5, insertText: 'Hi' },
    ]);
    assert.equal(store.getText(), 'Hi world!');
    const a = store.getAnnotation('w')!;
    assert.equal(store.getText().slice(a.start, a.end), 'world');
    assert.equal(store.undo(), true);
    assert.equal(store.getText(), 'hello world');
    assert.deepEqual(
      [store.getAnnotation('w')!.start, store.getAnnotation('w')!.end],
      [6, 11],
    );
  });
});

describe('undo', () => {
  it('restores the original anchors and text', () => {
    const store = new AnnotationStore('abcdef');
    store.addAnnotation(makeAnnotation({ startAffinity: 'outward' }));
    store.applyEdits([{ id: 1, offset: 2, deleteCount: 0, insertText: 'XY' }]);
    store.applyEdits([{ id: 2, offset: 0, deleteCount: 1 }]);
    assert.equal(store.undo(), true);
    assert.equal(store.undo(), true);
    assert.equal(store.getText(), 'abcdef');
    assert.deepEqual(
      [store.getAnnotation('a1')!.start, store.getAnnotation('a1')!.end],
      [2, 4],
    );
    assert.equal(store.undo(), false);
  });
});

describe('UTF-16 offsets', () => {
  const text = 'a\u{1F600}bc'; // 'a' + emoji (2 code units) + 'bc' => length 5

  it('offsets count UTF-16 code units', () => {
    assert.equal(text.length, 5);
    const store = new AnnotationStore(text);
    store.addAnnotation(makeAnnotation({ start: 1, end: 3 })); // exactly the emoji
    store.applyEdits([{ id: 1, offset: 5, deleteCount: 0, insertText: '!' }]);
    const a = store.getAnnotation('a1')!;
    assert.deepEqual([a.start, a.end], [1, 3]);
    assert.equal(store.getText().slice(a.start, a.end), '\u{1F600}');
  });

  it('rejects edits that would split a surrogate pair', () => {
    const store = new AnnotationStore(text);
    assert.throws(
      () => store.applyEdits([{ id: 1, offset: 2, deleteCount: 0, insertText: 'x' }]),
      RangeError,
    );
    assert.throws(
      () => store.applyEdits([{ id: 1, offset: 1, deleteCount: 1 }]),
      RangeError,
    );
    // The failed batch must not have modified the document.
    assert.equal(store.getText(), text);
  });

  it('rejects annotations whose anchors split a surrogate pair', () => {
    const store = new AnnotationStore(text);
    assert.throws(
      () => store.addAnnotation(makeAnnotation({ start: 2, end: 4 })),
      RangeError,
    );
  });

  it('isUtf16Boundary identifies pair interiors', () => {
    assert.equal(isUtf16Boundary(text, 1), true);
    assert.equal(isUtf16Boundary(text, 2), false);
    assert.equal(isUtf16Boundary(text, 3), true);
    assert.equal(isUtf16Boundary(text, 6), false);
  });
});

describe('line endings (LF and CRLF)', () => {
  it('position/offset conversion round-trips with LF', () => {
    const text = 'one\ntwo\nthree';
    assert.equal(offsetAtPosition(text, { line: 1, character: 0 }), 4);
    assert.equal(offsetAtPosition(text, { line: 2, character: 2 }), 10);
    assert.deepEqual(positionAtOffset(text, 10), { line: 2, character: 2 });
    assert.deepEqual(positionAtOffset(text, 3), { line: 0, character: 3 });
  });

  it('position/offset conversion round-trips with CRLF', () => {
    const text = 'one\r\ntwo\r\nthree';
    assert.equal(offsetAtPosition(text, { line: 1, character: 0 }), 5);
    assert.equal(offsetAtPosition(text, { line: 2, character: 2 }), 12);
    assert.deepEqual(positionAtOffset(text, 12), { line: 2, character: 2 });
    assert.deepEqual(positionAtOffset(text, 4), { line: 0, character: 3 });
  });

  it('annotations track edits across LF documents', () => {
    const store = new AnnotationStore('one\ntwo\nthree');
    store.addAnnotation(makeAnnotation({ id: 'L', start: 4, end: 7 })); // "two"
    store.applyEdits([{ id: 1, offset: 0, deleteCount: 0, insertText: 'ZERO\n' }]);
    const a = store.getAnnotation('L')!;
    assert.equal(store.getText().slice(a.start, a.end), 'two');
  });

  it('annotations track edits across CRLF documents', () => {
    const store = new AnnotationStore('one\r\ntwo\r\nthree');
    store.addAnnotation(makeAnnotation({ id: 'L', start: 5, end: 8 })); // "two"
    store.applyEdits([{ id: 1, offset: 0, deleteCount: 4 }]); // remove "one\r"
    assert.equal(store.getText(), '\ntwo\r\nthree');
    const a = store.getAnnotation('L')!;
    assert.equal(store.getText().slice(a.start, a.end), 'two');
  });

  it('deleting a CRLF sequence shifts anchors by two code units', () => {
    const store = new AnnotationStore('ab\r\ncd');
    store.addAnnotation(makeAnnotation({ id: 'L', start: 4, end: 6 })); // "cd"
    store.applyEdits([{ id: 1, offset: 2, deleteCount: 2 }]); // remove "\r\n"
    assert.equal(store.getText(), 'abcd');
    const a = store.getAnnotation('L')!;
    assert.deepEqual([a.start, a.end], [2, 4]);
    assert.equal(store.getText().slice(a.start, a.end), 'cd');
  });
});
