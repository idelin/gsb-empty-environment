import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GestureRecognizer } from '../src/recognizer.ts';
import { buildFixtureTree } from '../src/fixtures.ts';
import { hitTest } from '../src/layout.ts';
import type { GestureOutput, PointerInput, PointerEventType } from '../src/types.ts';

const P = (type: PointerEventType, pointerId: number, x: number, y: number, t: number, epoch = 0): PointerInput =>
  ({ type, pointerId, x, y, t, epoch });

function feed(rec: GestureRecognizer, events: PointerInput[]): GestureOutput[] {
  return events.flatMap((e) => rec.handle(e));
}

const kinds = (outs: GestureOutput[]) => outs.map((o) => (o.kind === 'commit' || o.kind === 'cancel' || o.kind === 'preview' || o.kind === 'compensate' ? `${o.kind}:${o.gesture}` : o.kind));

test('click commits within thresholds and reports the fixture hit target', () => {
  const tree = buildFixtureTree();
  const rec = new GestureRecognizer({ hitTest: (x, y) => hitTest(tree, x, y) });
  const outs = feed(rec, [P('down', 1, 50, 180, 0), P('up', 1, 50, 180, 120)]);
  assert.deepEqual(kinds(outs), ['commit:click']);
  const commit = outs[0];
  assert.equal(commit.kind, 'commit');
  if (commit.kind === 'commit') {
    assert.equal(commit.targetId, 'button-label');
    assert.equal(commit.duration, 120);
  }
  assert.ok(rec.isIdle());
});

test('slop boundary: dist == slop still clicks, slop+1 drags and never clicks', () => {
  for (const [dist, expected] of [[9, 'commit:click'], [10, 'commit:click'], [11, 'commit:drag']] as const) {
    const rec = new GestureRecognizer();
    const outs = feed(rec, [P('down', 1, 0, 0, 0), P('move', 1, dist, 0, 100), P('up', 1, dist, 0, 200)]);
    const commits = outs.filter((o) => o.kind === 'commit');
    assert.equal(commits.length, 1, `dist=${dist}`);
    assert.equal(kinds(commits)[0], expected);
    assert.ok(!kinds(outs).includes('commit:click') || expected === 'commit:click');
  }
});

test('timeout boundary: dur == timeout clicks, timeout+1 cancels', () => {
  for (const [dur, expected] of [[299, 'commit:click'], [300, 'commit:click'], [301, 'cancel:click']] as const) {
    const rec = new GestureRecognizer();
    const outs = feed(rec, [P('down', 1, 0, 0, 0), P('up', 1, 0, 0, dur)]);
    assert.equal(kinds(outs)[0], expected, `dur=${dur}`);
  }
});

test('click promoted to drag: click is cancelled, previews sum to commit totals', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [
    P('down', 1, 100, 100, 0),
    P('move', 1, 108, 104, 50),
    P('move', 1, 120, 110, 100), // total (20,10) > slop -> drag starts here
    P('move', 1, 135, 118, 150),
    P('up', 1, 135, 118, 200),
  ]);
  assert.ok(kinds(outs).includes('cancel:click'));
  assert.ok(!kinds(outs).includes('commit:click'), 'no click after drag');
  const previews = outs.filter((o) => o.kind === 'preview' && o.gesture === 'drag');
  const sumX = previews.reduce((a, p) => a + (p.kind === 'preview' ? p.dx : 0), 0);
  const sumY = previews.reduce((a, p) => a + (p.kind === 'preview' ? p.dy : 0), 0);
  const commit = outs.find((o) => o.kind === 'commit');
  assert.ok(commit && commit.kind === 'commit' && commit.gesture === 'drag');
  if (commit.kind === 'commit') {
    assert.equal(commit.dx, 35);
    assert.equal(commit.dy, 18);
    assert.equal(sumX, commit.dx);
    assert.equal(sumY, commit.dy);
  }
});

test('second pointer joins mid-drag: drag is compensated and cancelled, exactly one commit (pinch)', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [
    P('down', 1, 50, 50, 0),
    P('move', 1, 70, 50, 50), // drag, preview dx=20
    P('down', 2, 150, 50, 100), // -> compensate drag, cancel drag, pinch
    P('move', 2, 130, 50, 150), // pinch preview, scale 80/100
    P('up', 1, 70, 50, 200), // pinch commits
    P('up', 2, 130, 50, 250), // inert release
  ]);
  const k = kinds(outs);
  assert.deepEqual(k, ['cancel:click', 'preview:drag', 'compensate:drag', 'cancel:drag', 'preview:pinch', 'commit:pinch']);
  const comp = outs.find((o) => o.kind === 'compensate');
  assert.ok(comp && comp.kind === 'compensate');
  if (comp.kind === 'compensate') assert.equal(comp.dx, -20);
  assert.equal(outs.filter((o) => o.kind === 'commit').length, 1);
  assert.ok(rec.isIdle());
});

test('pinch scale is the ratio of finger spread', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [
    P('down', 1, 50, 50, 0),
    P('down', 2, 150, 50, 50), // spread 100
    P('move', 2, 200, 50, 100), // spread 150 -> scale 1.5
    P('move', 1, 25, 50, 150), // spread 175 -> scale 175/150
    P('up', 1, 25, 50, 200),
    P('up', 2, 200, 50, 250),
  ]);
  const commit = outs.find((o) => o.kind === 'commit');
  assert.ok(commit && commit.kind === 'commit' && commit.gesture === 'pinch');
  if (commit.kind === 'commit') assert.ok(Math.abs(commit.scale - 1.75) < 1e-9);
});

test('cancel mid-drag: previews are compensated, nothing commits, recognizer idles', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [P('down', 1, 0, 0, 0), P('move', 1, 30, 0, 50), P('cancel', 1, 30, 0, 100)]);
  assert.deepEqual(kinds(outs), ['cancel:click', 'preview:drag', 'compensate:drag', 'cancel:drag']);
  assert.ok(rec.isIdle());
});

test('lostcapture inside pinch: pinch compensated+cancelled, remaining pointer inert, never stuck active', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [
    P('down', 1, 50, 50, 0),
    P('down', 2, 150, 50, 50),
    P('move', 2, 120, 50, 100),
    P('lostcapture', 1, 50, 50, 150),
  ]);
  assert.deepEqual(kinds(outs), ['cancel:click', 'preview:pinch', 'compensate:pinch', 'cancel:pinch']);
  assert.equal(rec.getPhase(), 'settled');
  // remaining pointer cannot resurrect a gesture; its release returns to idle
  const rest = feed(rec, [P('move', 2, 160, 60, 200), P('up', 2, 160, 60, 250)]);
  assert.deepEqual(rest, []);
  assert.ok(rec.isIdle());
  // and a stray event for the lost pointer is ignored
  const stray = rec.handle(P('move', 1, 55, 55, 300));
  assert.equal(stray[0].kind, 'ignored');
});

test('coordinate-space change re-baselines instead of emitting a jump', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [
    P('down', 1, 50, 50, 0),
    P('move', 1, 55, 50, 50),
    P('move', 1, 1000, 1000, 100, 1), // epoch bump: no 945px jump
    P('move', 1, 1012, 1000, 150, 1), // 12px in new space -> drag
    P('up', 1, 1012, 1000, 200, 1),
  ]);
  const commit = outs.find((o) => o.kind === 'commit');
  assert.ok(commit && commit.kind === 'commit' && commit.gesture === 'drag');
  if (commit.kind === 'commit') {
    assert.equal(commit.dx, 12);
    assert.equal(commit.dy, 0);
  }
});

test('delayed and unknown events are ignored without disturbing the session', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [
    P('down', 1, 0, 0, 0),
    P('move', 1, 5, 0, 100),
    P('move', 1, 4, 0, 90), // out of order
    P('move', 9, 500, 500, 110), // unknown pointer
    P('up', 1, 5, 0, 200),
    P('move', 1, 50, 0, 250), // stray after release
  ]);
  assert.equal(outs.filter((o) => o.kind === 'ignored').length, 3);
  assert.deepEqual(kinds(outs).filter((k) => k.startsWith('commit')), ['commit:click']);
});

test('after pinch, lifting one finger never produces a click for the other', () => {
  const rec = new GestureRecognizer();
  const outs = feed(rec, [
    P('down', 1, 50, 50, 0),
    P('down', 2, 150, 50, 50),
    P('move', 2, 180, 50, 100),
    P('up', 2, 180, 50, 150),
    P('up', 1, 50, 50, 200),
  ]);
  assert.deepEqual(kinds(outs).filter((k) => k.startsWith('commit')), ['commit:pinch']);
});
