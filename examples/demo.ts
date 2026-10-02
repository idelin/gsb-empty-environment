import { CircularSignalUpdate, createReactiveSystem } from '../src/index.ts';

console.log('=== 1. normal reactive usage ===');
const app = createReactiveSystem();
const count = new app.Signal(0, 'count');
const doubled = new app.Computed(() => count.value * 2, 'doubled');
const logger = new app.Effect(() => {
  console.log(`count = ${count.value}, doubled = ${doubled.value}`);
}, 'logger');
count.value = 1;
count.value = 2;
logger.dispose();

console.log('\n=== 2. malicious effect: reads A and writes A ===');
const guarded = createReactiveSystem({ maxIterations: 50 });
const A = new guarded.Signal(0, 'A');
try {
  new guarded.Effect(() => {
    A.value = A.value + 1;
  }, 'effect-A');
  console.log('unreachable: the guard did not fire');
} catch (err) {
  if (err instanceof CircularSignalUpdate) {
    console.error('guard intercepted the loop:\n');
    console.error(err.message);
  } else {
    throw err;
  }
}

console.log('\n=== 3. computed call-stack cycle ===');
const cyc = createReactiveSystem();
const C1 = new cyc.Computed((): number => C2.value + 1, 'C1');
const C2 = new cyc.Computed((): number => C1.value + 1, 'C2');
try {
  C1.value;
} catch (err) {
  if (err instanceof CircularSignalUpdate) {
    console.error('guard intercepted the loop:\n');
    console.error(err.message);
  } else {
    throw err;
  }
}
