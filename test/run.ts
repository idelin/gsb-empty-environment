import { runUnitTests } from './unit';
import { runDifferentialTests } from './differential';

const start = Date.now();
const unit = runUnitTests();
console.log(`单元测试通过：${unit} 例`);

const seed = Number(process.env.SEED ?? 20261006);
const cases = Number(process.env.CASES ?? 3000);
const diff = runDifferentialTests(seed, cases);
console.log(`差分测试通过：${diff} 例（种子 ${seed}）`);

console.log(`全部通过，耗时 ${Date.now() - start}ms`);
