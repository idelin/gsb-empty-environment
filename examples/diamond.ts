/**
 * 钻石依赖演示：
 *
 *       A (signal)
 *      / \
 *     B   C   (computed)
 *      \ /
 *       D     (computed)
 *       |
 *    Effect
 *
 * 运行：node --experimental-strip-types examples/diamond.ts
 */
import { signal, computed, effect, batch } from '../src/reactive.ts';

const a = signal(1);

const b = computed(() => {
  const v = a.value * 2;
  console.log(`  [compute] B = A * 2   = ${v}  (version=${b.version})`);
  return v;
});

const c = computed(() => {
  const v = a.value + 10;
  console.log(`  [compute] C = A + 10  = ${v}  (version=${c.version})`);
  return v;
});

const d = computed(() => {
  const bv = b.value;
  const cv = c.value;
  console.log(
    `  [compute] D = B + C   = ${bv + cv}  (读到 B=${bv}@v${b.version}, C=${cv}@v${c.version})`,
  );
  return bv + cv;
});

console.log('--- 初始结算（effect 创建即触发首次计算）---');
const dispose = effect(() => {
  console.log(`  [effect ] D 当前值 = ${d.value}`);
});
console.log(`D = ${d.value}\n`);

console.log('--- 写入 A = 2 ---');
a.value = 2;
console.log(
  `结果: B 计算 ${b.computeCount} 次, C 计算 ${c.computeCount} 次, D 计算 ${d.computeCount} 次, D = ${d.value}\n`,
);

console.log('--- batch 写入 A = 3, A = 4 ---');
batch(() => {
  a.value = 3;
  a.value = 4;
});
console.log(
  `结果: B 计算 ${b.computeCount} 次, C 计算 ${c.computeCount} 次, D 计算 ${d.computeCount} 次, D = ${d.value}`,
);

dispose();
