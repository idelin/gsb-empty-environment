/**
 * Finite-state exploration: enumerate all short event sequences (BFS up to a
 * fixed depth) plus seeded random longer ones, and after EVERY event check:
 *
 *  I1. The editor's formal text equals an independent shadow model's text.
 *  I2. The projection equals the shadow projection (dual-lane consistency).
 *  I3. isComposing / canUndo / canRedo agree with the shadow model.
 *  I4. Stale-generation callbacks (update/commit/cancel) return false and
 *      change nothing (old callbacks cannot pollute a new composition).
 *  I5. Commit happens at most once per generation: after a successful
 *      commit, repeating it never changes the formal text.
 *  I6. Cancel never modifies the formal document, so it can never undo
 *      remote content.
 *  I7. The formal undo history contains no composition intermediate states:
 *      history length only grows on commit/local/remote edits, and a full
 *      undo-then-redo round trip restores the current formal text exactly.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Editor } from '../src/editor.ts';

// ---- independent shadow model ----------------------------------------------
// A second, deliberately simple implementation of the SAME public rules
// (R1-R7). Divergence between editor and shadow fails the test.

interface ShadowEntry {
  start: number;
  end: number;
  text: string;
  replaced: string;
}

interface ShadowSession {
  start: number;
  end: number;
  projection: string;
}

function conflict(a: number, b: number, s: number, e: number): boolean {
  return s < b && a < e;
}

function xStart(p: number, s: number, e: number, ins: number): number {
  if (p < s) return p;
  if (p >= e) return p + (ins - (e - s));
  return s;
}

function xEnd(p: number, s: number, e: number, ins: number): number {
  if (p <= s) return p;
  if (p > e) return p + (ins - (e - s));
  return s;
}

class Shadow {
  formal = 'AB'; // initial document shared with the editor under test
  undo: ShadowEntry[] = [];
  redo: ShadowEntry[] = [];
  session: ShadowSession | null = null;
  gen = 0;
  caret = 2; // collapsed selection; the editor is re-synced to it on `start`

  projection(): string {
    if (!this.session) return this.formal;
    const s = this.session;
    return this.formal.slice(0, s.start) + s.projection + this.formal.slice(s.end);
  }

  private apply(s: number, e: number, text: string): void {
    const replaced = this.formal.slice(s, e);
    this.undo.push({ start: s, end: e, text, replaced });
    this.redo = [];
    this.formal = this.formal.slice(0, s) + text + this.formal.slice(e);
    this.caret = s + text.length;
    this.reconcile(s, e, text.length);
  }

  private reconcile(s: number, e: number, ins: number): void {
    const sess = this.session;
    if (!sess) return;
    if (conflict(sess.start, sess.end, s, e)) {
      this.session = null; // R5: external edit wins
      return;
    }
    if (sess.start === sess.end) {
      sess.start = xStart(sess.start, s, e, ins);
      sess.end = sess.start;
    } else {
      sess.start = xStart(sess.start, s, e, ins);
      sess.end = xEnd(sess.end, s, e, ins);
    }
  }

  start(): number {
    this.session = null; // R6
    this.gen += 1;
    this.session = { start: this.caret, end: this.caret, projection: '' };
    return this.gen;
  }

  update(id: number, text: string): boolean {
    if (!this.session || id !== this.gen) return false;
    this.session.projection = text;
    return true;
  }

  commit(id: number, text?: string): boolean {
    if (!this.session || id !== this.gen) return false;
    const sess = this.session;
    this.session = null;
    this.apply(sess.start, sess.end, text ?? sess.projection);
    return true;
  }

  cancel(id: number): boolean {
    if (!this.session || id !== this.gen) return false;
    this.session = null; // formal text untouched (R4)
    return true;
  }

  blur(): void {
    this.session = null;
  }

  remote(at: number, text: string): void {
    this.apply(at, at, text);
  }

  conflictReplace(): void {
    const at = this.session ? this.session.start : 0;
    const from = Math.max(0, at - 1);
    const to = Math.min(this.formal.length, at + 1);
    this.apply(from, to, 'C');
  }

  undoOnce(): boolean {
    const en = this.undo.pop();
    if (!en) return false;
    this.redo.push(en);
    this.formal =
      this.formal.slice(0, en.start) +
      en.replaced +
      this.formal.slice(en.start + en.text.length);
    this.caret = Math.min(this.caret, this.formal.length);
    this.reconcile(en.start, en.start + en.text.length, en.replaced.length);
    return true;
  }

  redoOnce(): boolean {
    const en = this.redo.pop();
    if (!en) return false;
    this.undo.push(en);
    this.formal =
      this.formal.slice(0, en.start) +
      en.text +
      this.formal.slice(en.start + en.replaced.length);
    this.caret = Math.min(this.caret, this.formal.length);
    this.reconcile(en.start, en.start + en.replaced.length, en.text.length);
    return true;
  }
}

// ---- event alphabet --------------------------------------------------------

type Ev =
  | { t: 'start' }
  | { t: 'update' }
  | { t: 'staleUpdate' }
  | { t: 'commit' }
  | { t: 'commitAgain' }
  | { t: 'cancel' }
  | { t: 'staleCancel' }
  | { t: 'remote' }
  | { t: 'conflict' }
  | { t: 'undo' }
  | { t: 'redo' }
  | { t: 'blur' };

const ALPHABET: Ev[] = [
  { t: 'start' },
  { t: 'update' },
  { t: 'staleUpdate' },
  { t: 'commit' },
  { t: 'commitAgain' },
  { t: 'cancel' },
  { t: 'staleCancel' },
  { t: 'remote' },
  { t: 'conflict' },
  { t: 'undo' },
  { t: 'redo' },
  { t: 'blur' },
];

/** Apply one event to (editor, shadow) and check every invariant. */
function step(ed: Editor, sh: Shadow, ev: Ev, ctx: { lastGen: number }, label: string): void {
  const formalBefore = ed.getText();
  const histBefore = ed.historyKinds().length;

  switch (ev.t) {
    case 'start': {
      ed.setSelection(sh.caret, sh.caret);
      const id = ed.compositionStart();
      const sid = sh.start();
      assert.equal(id, sid, `${label}: generation ids agree`);
      ctx.lastGen = id;
      break;
    }
    case 'update': {
      const okE = ed.compositionUpdate(ctx.lastGen, 'う');
      const okS = sh.update(ctx.lastGen, 'う');
      assert.equal(okE, okS, `${label}: update acceptance agrees`);
      break;
    }
    case 'staleUpdate': {
      const ok = ed.compositionUpdate(ctx.lastGen - 1, 'POLLUTE');
      assert.equal(ok, false, `${label}: stale update rejected (I4)`);
      assert.equal(ed.getText(), formalBefore, `${label}: stale update changes nothing`);
      break;
    }
    case 'commit': {
      const okE = ed.compositionCommit(ctx.lastGen);
      const okS = sh.commit(ctx.lastGen);
      assert.equal(okE, okS, `${label}: commit acceptance agrees`);
      if (okE) {
        // I5: an immediate duplicate commit must be a no-op.
        const t = ed.getText();
        assert.equal(ed.compositionCommit(ctx.lastGen), false);
        assert.equal(ed.getText(), t, `${label}: duplicate commit inserts nothing (I5)`);
      }
      break;
    }
    case 'commitAgain': {
      // A second commit for the current generation: if a composition is
      // active it commits (with explicit text), otherwise it is a no-op.
      // Either way, a THIRD commit must never change anything (I5).
      const okE = ed.compositionCommit(ctx.lastGen, 'DUP');
      const okS = sh.commit(ctx.lastGen, 'DUP');
      assert.equal(okE, okS, `${label}: commitAgain acceptance agrees`);
      const t = ed.getText();
      assert.equal(ed.compositionCommit(ctx.lastGen), false);
      assert.equal(ed.compositionCommit(ctx.lastGen, 'DUP'), false);
      assert.equal(ed.getText(), t, `${label}: repeated commit inserts nothing (I5)`);
      break;
    }
    case 'cancel': {
      const okE = ed.compositionCancel(ctx.lastGen);
      const okS = sh.cancel(ctx.lastGen);
      assert.equal(okE, okS, `${label}: cancel acceptance agrees`);
      if (okE) {
        assert.equal(ed.getText(), formalBefore, `${label}: cancel never touches formal text (I6)`);
        assert.equal(ed.historyKinds().length, histBefore, `${label}: cancel pushes no history (I7)`);
      }
      break;
    }
    case 'staleCancel': {
      const ok = ed.compositionCancel(ctx.lastGen - 1);
      assert.equal(ok, false, `${label}: stale cancel rejected (I4)`);
      assert.equal(ed.getText(), formalBefore);
      break;
    }
    case 'remote': {
      const at = sh.session ? sh.session.start : 0; // boundary insert: non-conflict by rule
      ed.applyRemoteEdit({ start: at, end: at, text: 'R' });
      sh.remote(at, 'R');
      break;
    }
    case 'conflict': {
      const s = ed.getCompositionRange();
      const at = s ? s.start : 0;
      const from = Math.max(0, at - 1);
      const to = Math.min(ed.getText().length, at + 1);
      ed.applyRemoteEdit({ start: from, end: to, text: 'C' });
      sh.conflictReplace();
      break;
    }
    case 'undo': {
      const okE = ed.undo();
      const okS = sh.undoOnce();
      assert.equal(okE, okS, `${label}: undo availability agrees (I3)`);
      break;
    }
    case 'redo': {
      const okE = ed.redo();
      const okS = sh.redoOnce();
      assert.equal(okE, okS, `${label}: redo availability agrees (I3)`);
      break;
    }
    case 'blur': {
      ed.blur();
      sh.blur();
      break;
    }
  }

  // I1/I2/I3: cross-check the two implementations.
  assert.equal(ed.getText(), sh.formal, `${label}: formal text matches shadow (I1)`);
  assert.equal(ed.getProjection(), sh.projection(), `${label}: projection matches shadow (I2)`);
  assert.equal(ed.isComposing(), sh.session !== null, `${label}: composing flag agrees (I3)`);
  assert.equal(ed.canUndo(), sh.undo.length > 0, `${label}: canUndo agrees (I3)`);
  assert.equal(ed.canRedo(), sh.redo.length > 0, `${label}: canRedo agrees (I3)`);

  // I7: formal undo history is consistent and free of intermediate states.
  if (!ed.isComposing()) {
    const now = ed.getText();
    const redoable = ed.canRedo();
    let depth = 0;
    while (ed.canUndo() && depth < 100) {
      ed.undo();
      depth++;
    }
    for (let i = 0; i < depth; i++) assert.equal(ed.redo(), true);
    assert.equal(ed.getText(), now, `${label}: undo/redo round trip is exact (I7)`);
    assert.equal(ed.canRedo(), redoable, `${label}: redo stack preserved (I7)`);
  }
}

function fresh(): { ed: Editor; sh: Shadow; ctx: { lastGen: number } } {
  const ed = new Editor('AB');
  ed.setSelection(2, 2);
  return { ed, sh: new Shadow(), ctx: { lastGen: 0 } };
}

test('exhaustive exploration of all event sequences up to depth 4', () => {
  let count = 0;
  const walk = (seq: Ev[], depth: number): void => {
    const { ed, sh, ctx } = fresh();
    const label = `seq=[${seq.map((e) => e.t).join(',')}]`;
    for (const ev of seq) step(ed, sh, ev, ctx, label);
    count++;
    if (depth === 0) return;
    for (const ev of ALPHABET) walk([...seq, ev], depth - 1);
  };
  walk([], 4);
  assert.ok(count > 20000, `explored ${count} sequences`);
});

test('seeded random exploration of longer sequences (length 15)', () => {
  // mulberry32 PRNG, deterministic seeds.
  const rand = (seed: number) => () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  for (let seed = 1; seed <= 300; seed++) {
    const rng = rand(seed);
    const { ed, sh, ctx } = fresh();
    const seq: string[] = [];
    for (let i = 0; i < 15; i++) {
      const ev = ALPHABET[Math.floor(rng() * ALPHABET.length)];
      seq.push(ev.t);
      step(ed, sh, ev, ctx, `seed=${seed} seq=[${seq.join(',')}]`);
    }
  }
});
