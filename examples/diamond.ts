/**
 * 钻石依赖演示：
 *
 *     A
 *    / \
 *   B   C
 *    \ /
 *     D
 *
 * 运行：node examples/diamond.ts   （需 Node >= 23.6，原生运行 TypeScript）
 */
import { signal, computed, effect, batch } from "../src/reactive.ts";

const a = signal(1);

const b = computed(() => {
  const v = a.value + 1;
  console.log(`  [compute] B = A + 1 = ${v}`);
  return v;
});
const c = computed(() => {
  const v = a.value * 2;
  console.log(`  [compute] C = A * 2 = ${v}`);
  return v;
});
const d = computed(() => {
  const bv = b.value;
  const cv = c.value;
  console.log(`  [compute] D = B + C = ${bv} + ${cv} = ${bv + cv}  <- D 读到的 B/C 必为同一版本`);
  return bv + cv;
});

effect(() => {
  console.log(`  [effect]  D = ${d.value}`);
});

console.log("--- A = 2（单次写入） ---");
a.value = 2;

console.log("--- batch: A = 3, A = 4（合并为一个更新周期） ---");
batch(() => {
  a.value = 3;
  a.value = 4;
});

console.log("--- 拓扑染色高度 ---");
void d.value;
console.log(`  level: A=${a.level} B=${b.level} C=${c.level} D=${d.level}`);
