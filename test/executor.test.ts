import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createExecutor } from '../src/executor.ts';
import { editorSpec, type EditorState } from '../src/editorSpec.ts';
import { stateHash, type Event, type Spec } from '../src/spec.ts';
import { makeDocumentFixture } from '../src/fixtures.ts';
import { layoutDocument } from '../src/layout.ts';
import { glyphMetrics } from '../src/fixtures.ts';

const ev = (type: string, payload?: unknown): Event => ({ type, payload });

test('illegal events keep diagnostics and never change state', () => {
  const exec = createExecutor(editorSpec);
  const before = stateHash(exec.getState());

  const r1 = exec.dispatch(ev('TYPE', { ch: '中' })); // no glyph metric
  const r2 = exec.dispatch(ev('NOPE')); // unknown event
  const r3 = exec.dispatch(ev('MOVE', { to: 999 })); // guard rejects
  exec.dispatch(ev('ENTER', { mode: 'readonly' }));
  const r4 = exec.dispatch(ev('TYPE', { ch: 'a' })); // wrong mode

  assert.equal(r1.accepted, false);
  assert.equal(r1.diagnostic?.kind, 'guard-rejected');
  assert.equal(r2.diagnostic?.kind, 'unknown-event');
  assert.equal(r3.diagnostic?.kind, 'guard-rejected');
  assert.equal(r4.diagnostic?.kind, 'mode-mismatch');
  // state untouched by every rejected event except the legal ENTER
  exec.dispatch(ev('ENTER', { mode: 'active.insert' }));
  assert.equal(stateHash(exec.getState()), before);
  assert.equal(exec.getDiagnostics().length, 4);
  assert.equal(exec.getTrace().length, 2); // only the two ENTERs recorded
});

test('invariant violation in an action is rejected without state change', () => {
  const broken: Spec<EditorState> = {
    ...editorSpec,
    events: editorSpec.events.map((def) =>
      def.name === 'MOVE'
        ? { ...def, guard: undefined } // drop the bounds guard
        : def,
    ),
  };
  const exec = createExecutor(broken);
  const before = stateHash(exec.getState());
  const r = exec.dispatch(ev('MOVE', { to: 999 }));
  assert.equal(r.accepted, false);
  assert.equal(r.diagnostic?.kind, 'invariant-violated');
  assert.match(r.diagnostic!.detail, /cursor-in-bounds/);
  assert.equal(stateHash(exec.getState()), before);
});

test('recording + replay reproduces identical state (determinism)', () => {
  const script: Event[] = [
    ev('MOVE', { to: 5 }),
    ev('TYPE', { ch: 'a' }),
    ev('TYPE', { ch: 'b' }),
    ev('BACKSPACE'),
    ev('FOCUS', { target: 'p2' }),
    ev('TYPE', { ch: ' ' }),
    ev('ENTER', { mode: 'active.select' }),
    ev('SELECT', { to: 2 }),
    ev('ENTER', { mode: 'dialog.confirm' }),
    ev('CONFIRM'),
    ev('RESIZE', { width: 40 }),
  ];
  const run = () => {
    const exec = createExecutor(editorSpec);
    for (const e of script) assert.equal(exec.dispatch(e).accepted, true, JSON.stringify(e));
    return exec;
  };
  const a = run();
  const b = run();
  // same initial state + same event sequence => identical result
  assert.equal(stateHash(a.getState()), stateHash(b.getState()));
  assert.deepEqual(a.getTrace(), b.getTrace());

  const replayed = a.replay(script);
  assert.equal(replayed.divergences.length, 0);
  assert.equal(stateHash(replayed.state), stateHash(a.getState()));
});

test('replay reports divergence when a target node is missing', () => {
  const exec = createExecutor(editorSpec);
  exec.dispatch(ev('FOCUS', { target: 'p2' }));
  exec.dispatch(ev('TYPE', { ch: 'a' }));
  const recorded = exec.getTrace().map((t) => t.event);

  // replay against a fixture where paragraph p2 no longer exists
  const shrunk: Spec<EditorState> = {
    ...editorSpec,
    initial: () => {
      const tree = makeDocumentFixture();
      const without = { ...tree, children: tree.children!.filter((c) => c.id !== 'p2') };
      return {
        ...editorSpec.initial(),
        tree: without,
        layout: layoutDocument(glyphMetrics, without, 120),
      };
    },
  };
  const exec2 = createExecutor(shrunk);
  const result = exec2.replay(recorded);
  assert.equal(result.divergences.length, 1);
  assert.equal(result.divergences[0].diagnostic.kind, 'missing-target');
  assert.match(result.divergences[0].diagnostic.detail, /'p2' not found/);
});

test('side-effect callback failure rolls back the transaction', () => {
  const committed: string[] = [];
  const exec = createExecutor(editorSpec, {
    effects: [
      (next, event) => {
        if (event.type === 'TYPE') throw new Error('persistence layer down');
        committed.push(event.type);
      },
    ],
  });
  const before = stateHash(exec.getState());
  const r = exec.dispatch(ev('TYPE', { ch: 'a' }));
  assert.equal(r.accepted, false);
  assert.equal(r.diagnostic?.kind, 'effect-failed');
  assert.match(r.diagnostic!.detail, /rolled back/);
  assert.equal(stateHash(exec.getState()), before); // state rolled back
  assert.equal(exec.getTrace().length, 0); // nothing recorded

  const ok = exec.dispatch(ev('MOVE', { to: 1 }));
  assert.equal(ok.accepted, true);
  assert.deepEqual(committed, ['MOVE']);
});
