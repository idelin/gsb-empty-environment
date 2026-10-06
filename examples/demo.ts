import { Debugger, runProgram } from '../src/index.ts';

const source = `fn fib(n) {
  if (n < 2) {
    return n;
  }
  return fib(n - 1) + fib(n - 2);
}

let total = 0;
let i = 0;
while (i < 6) {
  total = total + fib(i);
  i = i + 1;
}
print(total);
`;

console.log('== full run ==');
const full = runProgram(source);
console.log('output:', full.output);
console.log('statements executed:', full.trace.length);

console.log('\n== debug run: conditional breakpoint on `total = total + fib(i)` when i == 4 ==');
const dbg = new Debugger(source);
let targetId = 0;
for (const entry of runProgram(source).trace) {
  if (entry.text.startsWith('total = total + fib(i)')) {
    targetId = entry.nodeId;
    break;
  }
}
dbg.addBreakpoint(targetId, 'i == 4');

const pause = dbg.continue();
console.log('paused at:', JSON.stringify(pause));
console.log('total at pause:', dbg.getVariable('total'));

console.log('injecting total = 1000 and continuing...');
dbg.setVariable('total', 1000);
dbg.continue();
console.log('output after modification:', dbg.output);
console.log('condition errors:', dbg.conditionErrors);
