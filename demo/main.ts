import { run } from '../src/interpreter.ts';
import { Debugger } from '../src/debugger.ts';

const src = [
  'function fib(n: number): number {',
  '  if (n <= 1) {',
  '    return n;',
  '  }',
  '  return fib(n - 1) + fib(n - 2);',
  '}',
  'let sum: number = 0;',
  'for (let i: number = 1; i <= 6; i = i + 1) {',
  '  sum = sum + fib(i);',
  '}',
  'print(sum);',
].join('\n');

console.log('== full run ==');
console.log(run(src).output.join('\n'));

console.log('\n== step trace (first 12 pauses) ==');
const stepped = new Debugger(src);
for (let k = 0; k < 12 && !stepped.done; k++) {
  const info = stepped.step();
  console.log(`pause line=${info.line} kind=${info.kind}`);
}
console.log(`... (${stepped.trace.length} pauses so far)`);
while (!stepped.done) stepped.step();
console.log(`stepped run output: ${stepped.output.join('\n')} (${stepped.trace.length} pauses total)`);

console.log('\n== conditional breakpoint: sum += fib(i) when i === 4 ==');
const dbg = new Debugger(src);
const loopLine = 9;
dbg.setBreakpointAtLine(loopLine, 'i === 4');
const hit = dbg.continue();
console.log(`paused at line ${hit.line}, i=${dbg.getVariable('i')}, sum=${dbg.getVariable('sum')}`);
console.log('modifying sum -> 1000 and continuing');
dbg.setVariable('sum', 1000);
dbg.continue();
console.log(`final output: ${dbg.output.join('\n')}`);
console.log(`condition errors: ${dbg.conditionErrors.length}`);
