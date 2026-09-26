// Runnable sample: executor + checker + cross-validation + hot-swap.
// Run with: npm run demo   (node src/main.ts)

import { createExecutor, type Executor } from './executor.ts';
import { check } from './checker.ts';
import { crossValidate } from './crosscheck.ts';
import { editorSpec, type EditorState } from './editorSpec.ts';
import { cursorRect } from './layout.ts';
import { findNode, glyphMetrics } from './fixtures.ts';
import { stateHash, type Event } from './spec.ts';

const show = (title: string) => console.log(`\n=== ${title} ===`);

// --- 1. Executor: dispatch, invariants, diagnostics, recording ----------------
show('1. executor dispatch + diagnostics');
const sideEffects: string[] = [];
const exec: Executor<EditorState> = createExecutor(editorSpec, {
  effects: [
    (next, event) => {
      sideEffects.push(`committed ${event.type} -> ${stateHash(next)}`);
      if (event.type === 'RESIZE' && (event.payload as { width: number }).width === 40) {
        throw new Error('simulated persistence failure');
      }
    },
  ],
});

const script: Event[] = [
  { type: 'MOVE', payload: { to: 11 } },
  { type: 'TYPE', payload: { ch: '!' } },
  { type: 'TYPE', payload: { ch: '中' } },        // illegal: no glyph metric
  { type: 'BACKSPACE' },
  { type: 'ENTER', payload: { mode: 'readonly' } },
  { type: 'TYPE', payload: { ch: 'a' } },        // illegal: wrong mode
  { type: 'ENTER', payload: { mode: 'active.select' } },
  { type: 'SELECT', payload: { to: 2 } },
  { type: 'ENTER', payload: { mode: 'dialog.confirm' } }, // clears selection atomically
  { type: 'CONFIRM' },
  { type: 'RESIZE', payload: { width: 40 } },    // effect fails -> rollback
  { type: 'RESIZE', payload: { width: 60 } },
];
for (const event of script) {
  const r = exec.dispatch(event);
  console.log(`${r.accepted ? 'ok     ' : 'REJECT '} ${JSON.stringify(event)}`);
}
console.log('diagnostics:');
for (const d of exec.getDiagnostics()) {
  console.log(`  #${d.seq} [${d.kind}] ${d.detail}`);
}
const st = exec.getState();
console.log(`mode=${st.mode} focus=${st.focusId} cursor=${st.cursor} maxWidth=${st.maxWidth}`);
console.log(`side effects committed: ${sideEffects.length} (RESIZE 40 rolled back)`);

// --- 2. Layout core (pure, fixture-driven) ------------------------------------
show('2. pure layout from fixtures');
const p1 = findNode(st.tree, 'p1')!;
console.log(`p1 text: '${p1.text}' laid out at maxWidth=${st.maxWidth}`);
for (const line of st.layout.lines.filter((l) => l.nodeId === 'p1')) {
  console.log(`  line ${line.index}: [${line.start},${line.end}) width=${line.width}px y=${line.y}px`);
}
const rect = cursorRect(glyphMetrics, st.layout, st.focusId, p1.text ?? '', st.cursor);
console.log(`cursor at x=${rect.x}px y=${rect.y}px (line ${rect.lineIndex})`);

// --- 3. Recording + replay -----------------------------------------------------
show('3. record + replay');
const trace = exec.getTrace();
console.log(`recorded ${trace.length} accepted events`);
const replayed = exec.replay(trace.map((t) => t.event));
console.log(`replay divergences: ${replayed.divergences.length}`);
console.log(`replay final hash equals live hash: ${stateHash(replayed.state) === stateHash(exec.getState())}`);

// --- 4. Model checker ----------------------------------------------------------
show('4. model checker (finite domain, BFS => shortest counterexample)');
const report = check(editorSpec, { maxDepth: 6 });
console.log(`ok=${report.ok} explored=${report.exploredStates} states up to depth ${report.maxDepth}`);
if (report.counterexample) {
  console.log(`counterexample: ${report.counterexample.name}`);
  console.log(`trace: ${JSON.stringify(report.counterexample.trace)}`);
}

// --- 5. Cross-validation -------------------------------------------------------
show('5. executor trace vs checker one-step semantics');
const cross = crossValidate(editorSpec, trace);
console.log(`ok=${cross.ok} steps=${cross.steps} mismatches=${cross.mismatches.length}`);

// --- 6. Hot-swap with migration evidence ---------------------------------------
show('6. hot-swap spec');
const brokenSpec = {
  ...editorSpec,
  name: 'editor-v2-broken',
  invariants: [
    ...editorSpec.invariants,
    { name: 'cursor-must-be-zero', check: (s: EditorState) => (s.cursor === 0 ? null : 'cursor must be 0') },
  ],
};
const failed = exec.hotSwap(brokenSpec, (s) => s);
console.log(`broken swap ok=${failed.ok} error=${failed.error}`);
console.log(`spec still: ${exec.getSpec().name}, cursor still: ${exec.getState().cursor}`);

const v2Spec = { ...editorSpec, name: 'editor-v2' };
const migrated = exec.hotSwap(v2Spec, (s) => ({ ...s, cursor: Math.min(s.cursor, 5) }));
console.log(`good swap ok=${migrated.ok} evidence modeValid=${migrated.evidence.modeValid} invariants=${migrated.evidence.invariants.every((i) => i.ok)}`);
console.log(`spec now: ${exec.getSpec().name}`);
