import { runRules, type Rule, type RunResult } from '../src/engine.ts';
import type { JsonValue } from '../src/json.ts';

const replaceValue = (
  name: string,
  priority: number,
  from: JsonValue,
  to: JsonValue,
): Rule => ({
  name,
  priority,
  precondition: ({ value }) => value === from,
  fix: () => to,
});

function show(title: string, result: RunResult): void {
  console.log(`\n=== ${title} ===`);
  console.log(`status: ${result.status}`);
  for (const s of result.steps) {
    console.log(
      `  step ${s.seq} [${s.rule} prio=${s.priority}] ${s.path || '(root)'}: ${JSON.stringify(s.before)} -> ${JSON.stringify(s.after)}`,
    );
  }
  if (result.conflict) {
    console.log(`  conflict at ${result.conflict.path}, value=${JSON.stringify(result.conflict.value)}`);
    for (const c of result.conflict.competitors) {
      console.log(`    competitor: ${c.rule} (priority ${c.priority})`);
    }
  }
  if (result.oscillation) {
    console.log(
      `  oscillation: cycle of ${result.oscillation.cycleSteps.length} step(s) from seq ${result.oscillation.cycleStartSeq}, rules: ${result.oscillation.rulesInvolved.join(', ')}`,
    );
  }
  console.log(`output: ${JSON.stringify(result.output)}`);
}

// 1. Fixpoint: rule A rewrites every "x" to "y", then nothing matches anymore.
const input1 = { items: [{ name: 'x' }, { name: 'ok' }, { name: 'x' }] };
const r1 = runRules(input1, [replaceValue('A:x->y', 1, 'x', 'y')]);
show('1. fixpoint (A: x->y)', r1);
console.log(`rollback() === input1: ${r1.rollback() === input1}`);

// 2. Oscillation: A rewrites x->y, B rewrites y->x; the engine must stop.
const r2 = runRules({ v: 'x' }, [
  replaceValue('A:x->y', 1, 'x', 'y'),
  replaceValue('B:y->x', 2, 'y', 'x'),
]);
show('2. oscillation (A: x->y vs B: y->x)', r2);

// 3. Conflict: two distinct rules, same priority, same node.
const r3 = runRules({ v: 'x' }, [
  replaceValue('to-y', 1, 'x', 'y'),
  replaceValue('to-z', 1, 'x', 'z'),
]);
show('3. conflict (equal priority, same path)', r3);

// 4. Budget: a rule that always grows the array never reaches a fixpoint.
const grow: Rule = {
  name: 'grow',
  priority: 1,
  precondition: ({ path }) => path.length === 0,
  fix: (value) => [...(value as JsonValue[]), 'x'],
};
const r4 = runRules([], [grow], { budget: 4 });
show('4. budget exhausted (budget=4)', r4);

// 5. Precondition gate: rule never fires on a non-matching tree.
const r5 = runRules({ v: 'untouched' }, [replaceValue('A:x->y', 1, 'x', 'y')]);
show('5. precondition not satisfied -> no-op', r5);
