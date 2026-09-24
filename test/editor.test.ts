import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  Editor,
  EditConflictError,
  OffsetError,
  mapOffset,
  offsetToPosition,
  positionToOffset,
} from '../src/index.ts';

describe('baseline coordinate system', () => {
  it('applies multiple replacements against the original coordinates', () => {
    const editor = new Editor('alpha beta gamma delta');
    // Cursors on "beta", "gamma" and "delta"; the LATER replacements change
    // length drastically, which must not disturb the EARLIER ranges.
    editor.transaction((tx) => {
      tx.replace(6, 10, 'B'); // beta -> B (shorter)
      tx.replace(11, 16, 'GAMMA-GAMMA-GAMMA'); // gamma -> much longer
      tx.replace(17, 22, ''); // delete delta
    });
    assert.equal(editor.text, 'alpha B GAMMA-GAMMA-GAMMA ');
  });

  it('supports insert and delete sugar in the same transaction', () => {
    const editor = new Editor('hello world');
    editor.transaction((tx) => {
      tx.insert(0, '> ');
      tx.delete(5, 11);
      tx.insert(5, '!');
    });
    assert.equal(editor.text, '> hello!');
  });

  it('keeps cursors tracking text when earlier edits change length', () => {
    const editor = new Editor('aXbYc');
    editor.setSelections([
      { anchor: 4, head: 4 }, // caret after "c"
    ]);
    editor.transaction((tx) => {
      tx.replace(1, 2, 'XXXXX'); // X -> XXXXX, +4 before the caret
    });
    assert.equal(editor.text, 'aXXXXXbYc');
    assert.deepEqual(editor.selections, [{ anchor: 8, head: 8 }]);
  });
});

describe('conflict detection (fails before writing)', () => {
  it('rejects overlapping replacements and leaves text untouched', () => {
    const editor = new Editor('abcdefghij');
    assert.throws(
      () =>
        editor.transaction((tx) => {
          tx.replace(2, 6, 'X');
          tx.replace(4, 8, 'Y');
        }),
      EditConflictError,
    );
    assert.equal(editor.text, 'abcdefghij');
    assert.equal(editor.undoDepth, 0);
  });

  it('rejects an insertion strictly inside another edit range', () => {
    const editor = new Editor('abcdefghij');
    assert.throws(
      () =>
        editor.transaction((tx) => {
          tx.replace(2, 8, 'X');
          tx.insert(5, 'oops');
        }),
      EditConflictError,
    );
    assert.equal(editor.text, 'abcdefghij');
  });

  it('rejects two insertions at the same offset', () => {
    const editor = new Editor('abc');
    assert.throws(
      () =>
        editor.transaction((tx) => {
          tx.insert(1, 'x');
          tx.insert(1, 'y');
        }),
      EditConflictError,
    );
    assert.equal(editor.text, 'abc');
  });

  it('allows insertions at the boundary of a replacement', () => {
    const editor = new Editor('abcdefghij');
    editor.transaction((tx) => {
      tx.replace(3, 7, 'MID');
      tx.insert(3, '<'); // at the start boundary
      tx.insert(7, '>'); // at the end boundary
    });
    assert.equal(editor.text, 'abc<MID>hij');
  });

  it('allows adjacent non-overlapping replacements', () => {
    const editor = new Editor('aaaabbbb');
    editor.transaction((tx) => {
      tx.replace(0, 4, 'A');
      tx.replace(4, 8, 'B');
    });
    assert.equal(editor.text, 'AB');
  });
});

describe('UTF-16 offsets and surrogate pairs', () => {
  const smiley = 'a😀b'; // offsets: a=0, 😀=1..2 (pair), b=3

  it('rejects edit ranges that split a surrogate pair', () => {
    const editor = new Editor(smiley);
    assert.throws(
      () => editor.transaction((tx) => tx.replace(0, 2, 'x')),
      OffsetError,
    );
    assert.throws(
      () => editor.transaction((tx) => tx.insert(2, 'x')),
      OffsetError,
    );
    assert.equal(editor.text, smiley);
  });

  it('rejects selections that split a surrogate pair', () => {
    const editor = new Editor(smiley);
    assert.throws(
      () => editor.setSelections([{ anchor: 0, head: 2 }]),
      OffsetError,
    );
  });

  it('edits around an astral character using UTF-16 offsets', () => {
    const editor = new Editor(smiley);
    editor.transaction((tx) => {
      tx.replace(1, 3, '🎉'); // replace the emoji (2 UTF-16 units)
      tx.insert(4, '!'); // after "b" in baseline coordinates
    });
    assert.equal(editor.text, 'a🎉b!');
  });

  it('position conversion never lands inside a surrogate pair', () => {
    const text = '😀😀';
    assert.equal(positionToOffset(text, { line: 0, character: 1 }), 0);
    assert.equal(positionToOffset(text, { line: 0, character: 3 }), 2);
  });
});

describe('LF and CRLF line endings', () => {
  for (const [name, text] of [
    ['LF', 'one\ntwo\nthree'],
    ['CRLF', 'one\r\ntwo\r\nthree'],
  ] as const) {
    it(`round-trips positions with ${name} endings`, () => {
      const editor = new Editor(text);
      for (let offset = 0; offset <= text.length; offset++) {
        // Skip offsets inside the CRLF pair: not a valid public position.
        if (text[offset - 1] === '\r' && text[offset] === '\n') continue;
        const pos = editor.positionAt(offset);
        assert.equal(editor.offsetAt(pos), offset, `offset ${offset}`);
      }
    });
  }

  it('counts CRLF as two UTF-16 units in offsets', () => {
    const editor = new Editor('ab\r\ncd');
    assert.deepEqual(editor.positionAt(4), { line: 1, character: 0 });
    assert.equal(editor.offsetAt({ line: 1, character: 2 }), 6);
    // character past line content clamps to before the "\r\n"
    assert.equal(editor.offsetAt({ line: 0, character: 99 }), 2);
  });

  it('edits a CRLF document by line positions', () => {
    const editor = new Editor('foo\r\nbar\r\nbaz');
    editor.transaction((tx) => {
      const start = editor.offsetAt({ line: 1, character: 0 });
      const end = editor.offsetAt({ line: 1, character: 3 });
      tx.replace(start, end, 'BAR');
      tx.insert(editor.offsetAt({ line: 2, character: 3 }), '!');
    });
    assert.equal(editor.text, 'foo\r\nBAR\r\nbaz!');
  });

  it('maps offsets across a CRLF insertion', () => {
    const editor = new Editor('a\nb');
    editor.transaction((tx) => {
      tx.replace(1, 2, '\r\n\r\n'); // "\n" -> "\r\n\r\n"
    });
    assert.equal(editor.text, 'a\r\n\r\nb');
    assert.deepEqual(editor.positionAt(5), { line: 2, character: 0 });
  });
});

describe('selections and annotations inside a transaction', () => {
  it('maps existing selections and annotations through the edits', () => {
    const editor = new Editor('the quick brown fox');
    editor.setSelections([{ anchor: 16, head: 19 }]); // "fox"
    editor.addAnnotation({ id: 'adj', range: { start: 4, end: 9 } }); // quick
    editor.transaction((tx) => {
      tx.replace(0, 3, 'A'); // "the" -> "A": everything shifts left by 2
    });
    assert.equal(editor.text, 'A quick brown fox');
    assert.deepEqual(editor.selections, [{ anchor: 14, head: 17 }]);
    assert.deepEqual(editor.annotations[0].range, { start: 2, end: 7 });
  });

  it('accepts staged selections and annotations in baseline coordinates', () => {
    const editor = new Editor('hello world');
    editor.transaction((tx) => {
      tx.replace(6, 11, 'there beautiful world');
      tx.setSelections([{ anchor: 6, head: 6 }]); // baseline offset 6
      tx.addAnnotation({ id: 'greeting', range: { start: 0, end: 5 } });
    });
    assert.equal(editor.text, 'hello there beautiful world');
    assert.deepEqual(editor.selections, [{ anchor: 6, head: 6 }]);
    assert.deepEqual(editor.annotations[0].range, { start: 0, end: 5 });
  });

  it('removes annotations inside a transaction', () => {
    const editor = new Editor('abc');
    editor.addAnnotation({ id: 'x', range: { start: 0, end: 1 } });
    editor.addAnnotation({ id: 'y', range: { start: 1, end: 2 } });
    editor.transaction((tx) => {
      tx.removeAnnotation('x');
      tx.insert(3, 'd');
    });
    assert.deepEqual(
      editor.annotations.map((a) => a.id),
      ['y'],
    );
  });
});

describe('atomicity', () => {
  it('restores all four state categories when the callback throws', () => {
    const editor = new Editor('original text');
    editor.setSelections([{ anchor: 1, head: 4 }]);
    editor.addAnnotation({ id: 'note', range: { start: 0, end: 8 } });
    editor.transaction((tx) => tx.insert(0, 'ok ')); // one undo record
    const before = {
      text: editor.text,
      selections: editor.selections,
      annotations: editor.annotations,
      undoDepth: editor.undoDepth,
    };
    assert.throws(
      () =>
        editor.transaction((tx) => {
          tx.replace(0, 5, 'GARBAGE');
          tx.setSelections([{ anchor: 0, head: 0 }]);
          tx.removeAnnotation('note');
          throw new Error('boom');
        }),
      /boom/,
    );
    assert.equal(editor.text, before.text);
    assert.deepEqual(editor.selections, before.selections);
    assert.deepEqual(editor.annotations, before.annotations);
    assert.equal(editor.undoDepth, before.undoDepth);
  });

  it('rolls back when a late commit step fails (duplicate annotation id)', () => {
    const editor = new Editor('abc def');
    editor.addAnnotation({ id: 'dup', range: { start: 0, end: 3 } });
    assert.throws(
      () =>
        editor.transaction((tx) => {
          tx.replace(0, 3, 'ABC');
          tx.addAnnotation({ id: 'dup', range: { start: 4, end: 7 } });
        }),
      /duplicate annotation id/,
    );
    assert.equal(editor.text, 'abc def');
    assert.equal(editor.annotations.length, 1);
    assert.equal(editor.undoDepth, 0);
  });

  it('rolls back when a staged selection is invalid', () => {
    const editor = new Editor('short');
    assert.throws(
      () =>
        editor.transaction((tx) => {
          tx.insert(0, 'x');
          tx.setSelections([{ anchor: 0, head: 999 }]);
        }),
      OffsetError,
    );
    assert.equal(editor.text, 'short');
    assert.equal(editor.undoDepth, 0);
  });
});

describe('undo records', () => {
  it('undoes text, selections and annotations of a transaction', () => {
    const editor = new Editor('one two three');
    editor.setSelections([{ anchor: 0, head: 0 }]);
    editor.addAnnotation({ id: 'a', range: { start: 4, end: 7 } });
    editor.transaction((tx) => {
      tx.replace(4, 7, 'TWO');
      tx.setSelections([{ anchor: 13, head: 13 }]);
      tx.removeAnnotation('a');
      tx.addAnnotation({ id: 'b', range: { start: 0, end: 3 } });
    });
    assert.equal(editor.text, 'one TWO three');
    assert.equal(editor.undoDepth, 1);
    assert.equal(editor.undo(), true);
    assert.equal(editor.text, 'one two three');
    assert.deepEqual(editor.selections, [{ anchor: 0, head: 0 }]);
    assert.deepEqual(
      editor.annotations.map((a) => [a.id, a.range]),
      [['a', { start: 4, end: 7 }]],
    );
    assert.equal(editor.undo(), false);
  });

  it('undoes a multi-cursor length-changing transaction exactly', () => {
    const editor = new Editor('alpha beta gamma delta');
    editor.transaction((tx) => {
      tx.replace(6, 10, 'B');
      tx.replace(11, 16, 'GAMMA-GAMMA-GAMMA');
      tx.replace(17, 22, '');
    });
    assert.equal(editor.text, 'alpha B GAMMA-GAMMA-GAMMA ');
    assert.equal(editor.undo(), true);
    assert.equal(editor.text, 'alpha beta gamma delta');
  });

  it('undoes multiple transactions in LIFO order', () => {
    const editor = new Editor('abc');
    editor.transaction((tx) => tx.insert(3, 'd'));
    editor.transaction((tx) => tx.insert(4, 'e'));
    assert.equal(editor.text, 'abcde');
    editor.undo();
    assert.equal(editor.text, 'abcd');
    editor.undo();
    assert.equal(editor.text, 'abc');
  });
});

describe('mapOffset', () => {
  const edits = [{ range: { start: 2, end: 5 }, text: 'LONGER' }]; // +3

  it('shifts offsets after an edit by its delta', () => {
    assert.equal(mapOffset(0, edits), 0);
    assert.equal(mapOffset(6, edits), 9);
  });

  it('clamps offsets inside a replaced region', () => {
    assert.equal(mapOffset(3, edits), 2);
    assert.equal(mapOffset(3, edits, 'end'), 8);
  });

  it('honours association at boundaries', () => {
    assert.equal(mapOffset(2, edits), 2);
    assert.equal(mapOffset(2, edits, 'end'), 8);
    assert.equal(mapOffset(5, edits), 8); // end boundary follows the text
  });
});
