import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { BranchingHistory } from '../src/history.ts';

describe('BranchingHistory', () => {
  it('keeps ab and x as separate branches after undo and continued editing', () => {
    const history = new BranchingHistory();

    history.replace(0, 0, 'a', { anchor: 1, active: 1 });
    history.replace(1, 1, 'b', { anchor: 2, active: 2 });
    history.undo();
    history.replace(0, 0, 'x', { anchor: 1, active: 1 });

    assert.deepEqual(history.getSnapshot(), {
      text: 'x',
      selection: { anchor: 1, active: 1 },
    });
    assert.deepEqual(history.getBranches().map((branch) => branch.text), ['ab', 'x']);

    history.switchToBranch(0);
    assert.deepEqual(history.getSnapshot(), {
      text: 'ab',
      selection: { anchor: 2, active: 2 },
    });

    history.switchToBranch(1);
    assert.deepEqual(history.getSnapshot(), {
      text: 'x',
      selection: { anchor: 1, active: 1 },
    });
  });

  it('restores directed selections when switching branches', () => {
    const history = new BranchingHistory();
    history.replace(0, 0, 'ab');
    history.setSelection({ anchor: 0, active: 2 });
    history.undo();
    history.replace(0, 0, 'x', { anchor: 1, active: 0 });

    history.switchToBranch(0);
    assert.deepEqual(history.selection, { anchor: 0, active: 2 });
    history.switchToBranch(1);
    assert.deepEqual(history.selection, { anchor: 1, active: 0 });
  });

  it('coalesces continuous typing but not across cursor movement or transaction boundaries', () => {
    const history = new BranchingHistory();

    history.replace(0, 0, 'a');
    history.replace(1, 1, 'b');
    assert.equal(history.nodeCount, 2);
    assert.equal(history.text, 'ab');

    history.setSelection({ anchor: 1, active: 1 });
    history.replace(1, 1, 'c');
    assert.equal(history.nodeCount, 3);
    assert.equal(history.text, 'acb');

    history.beginTransaction();
    history.replace(3, 3, 'd');
    history.commitTransaction();
    assert.equal(history.nodeCount, 4);
    assert.equal(history.text, 'acbd');

    history.replace(4, 4, 'e');
    assert.equal(history.nodeCount, 5);
  });

  it('groups multiple changes inside one explicit transaction into one undo step', () => {
    const history = new BranchingHistory();

    history.beginTransaction();
    history.replace(0, 0, 'a');
    history.replace(1, 1, 'b');
    history.commitTransaction();

    assert.equal(history.nodeCount, 2);
    assert.equal(history.text, 'ab');
    history.undo();
    assert.equal(history.text, '');
  });

  it('does not coalesce typing after a rolled-back transaction boundary', () => {
    const history = new BranchingHistory();
    history.replace(0, 0, 'a');
    history.beginTransaction();
    history.replace(1, 1, 'b');
    history.rollbackTransaction();
    assert.equal(history.text, 'a');
    history.replace(1, 1, 'c');
    assert.equal(history.nodeCount, 3);
    assert.equal(history.text, 'ac');
  });

  it('uses UTF-16 code unit offsets and rejects edits that split surrogate pairs', () => {
    const emoji = '🙂';
    assert.equal(emoji.length, 2);

    const history = new BranchingHistory({ text: emoji });
    assert.throws(() => history.replace(1, 1, 'x'), /splits a UTF-16 surrogate pair/);

    history.replace(0, 2, 'x');
    assert.equal(history.text, 'x');

    const paired = new BranchingHistory({ text: `a${emoji}` });
    assert.throws(() => paired.replace(2, 2, 'b'), /splits a UTF-16 surrogate pair/);
    paired.replace(3, 3, 'b', { anchor: 4, active: 4 });
    assert.equal(paired.text, 'a🙂b');
    assert.deepEqual(paired.selection, { anchor: 4, active: 4 });

    assert.throws(
      () => new BranchingHistory({ text: emoji, selection: { anchor: 1, active: 1 } }),
      /selection\.anchor splits a UTF-16 surrogate pair/,
    );
    assert.throws(() => paired.setSelection({ anchor: 2, active: 2 }), /selection\.anchor splits/);
  });

  it('supports editing and undo across LF and CRLF text', () => {
    const lf = new BranchingHistory({ text: 'a\nb' });
    lf.replace(1, 1, '\r');
    assert.equal(lf.text, 'a\r\nb');
    lf.undo();
    assert.equal(lf.text, 'a\nb');

    const crlf = new BranchingHistory({ text: 'a\nb' });
    crlf.replace(1, 2, '\r\n');
    assert.equal(crlf.text, 'a\r\nb');
    crlf.undo();
    assert.equal(crlf.text, 'a\nb');

    const multiline = new BranchingHistory({ text: 'first\r\nsecond' });
    multiline.replace(7, 7, '\r\n');
    assert.equal(multiline.text, 'first\r\n\r\nsecond');
  });

  it('replays every visible node after capacity compaction', () => {
    const history = new BranchingHistory({ maxHistoryNodes: 2 });

    history.replace(0, 0, 'a');
    history.replace(1, 1, 'b');
    history.undo();
    history.replace(0, 0, 'x');

    assert.equal(history.nodeCount, 2);
    assert.deepEqual(history.getBranches().map((branch) => branch.text), ['ab', 'x']);
    assert.deepEqual(
      history.replayVisibleNodes().map((state) => [state.nodeId, state.text]),
      [
        ['n2', 'ab'],
        ['n3', 'x'],
      ],
    );
  });

  it('preserves shared and surviving branch baselines during compaction', () => {
    const history = new BranchingHistory({ maxHistoryNodes: 3 });

    history.replace(0, 0, 'a');
    history.setSelection({ anchor: 1, active: 1 });
    history.replace(1, 1, 'b');
    history.undo();
    history.replace(1, 1, 'c');

    assert.equal(history.nodeCount, 3);
    assert.deepEqual(
      history.replayVisibleNodes().map((state) => state.text),
      ['a', 'ab', 'ac'],
    );

    history.switchToBranch(0);
    assert.equal(history.text, 'ab');
    history.switchToBranch(1);
    assert.equal(history.text, 'ac');

    history.replace(2, 2, 'd');
    assert.equal(history.nodeCount, 3);
    assert.deepEqual(
      history.replayVisibleNodes().map((state) => state.text),
      ['ab', 'ac', 'acd'],
    );
  });

  it('switches to arbitrary visible nodes and replays their complete state', () => {
    const history = new BranchingHistory();
    history.replace(0, 0, 'a', { anchor: 1, active: 1 });
    const aNode = history.currentNodeId;
    history.setSelection({ anchor: 1, active: 1 });
    history.replace(1, 1, 'b', { anchor: 2, active: 2 });
    const abNode = history.currentNodeId;

    history.undo();
    history.replace(1, 1, 'x', { anchor: 2, active: 2 });

    history.switchTo(aNode);
    assert.deepEqual(history.getSnapshot(), {
      text: 'a',
      selection: { anchor: 1, active: 1 },
    });

    history.switchTo(abNode);
    assert.deepEqual(history.getSnapshot(), {
      text: 'ab',
      selection: { anchor: 2, active: 2 },
    });
  });

  it('keeps compacted branch baselines replayable and stable on undo', () => {
    const history = new BranchingHistory({ maxHistoryNodes: 3 });
    history.replace(0, 0, 'a');
    history.setSelection({ anchor: 1, active: 1 });
    history.replace(1, 1, 'b');
    history.undo();
    history.replace(1, 1, 'c');

    history.switchToBranch(0);
    assert.equal(history.text, 'ab');
    history.replace(2, 2, 'd');

    assert.equal(history.nodeCount, 3);
    assert.deepEqual(
      history.replayVisibleNodes().map((state) => state.text),
      ['ab', 'abd', 'ac'],
    );
    history.undo();
    assert.equal(history.text, 'ab');
    assert.deepEqual(
      history.replayVisibleNodes().map((state) => state.text),
      ['ab', 'abd', 'ac'],
    );
    assert.deepEqual(history.getBranches().map((branch) => branch.text).sort(), ['abd', 'ac']);
  });
});
