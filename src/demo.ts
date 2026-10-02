/**
 * 运行样例：模拟长尾延迟流 + 极端毛刺，实时输出滑动窗口 P90/P99，
 * 并与"完全保留全部窗口样本"的精确值对照。
 *
 * 运行：node src/demo.ts   （Node >= 22.18 / 23.6，原生运行 TypeScript）
 */
import { SlidingWindowHistogram } from "./slidingWindowHistogram.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rng = mulberry32(20261002);
const normal = () => {
  const u1 = Math.max(rng(), 1e-12);
  return Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * rng());
};
const baseline = () => 50 * Math.exp(0.6 * normal()); // 长尾基线，中位 50ms

const CAPACITY = 1000;
const hist = new SlidingWindowHistogram({ capacity: CAPACITY });
const window: number[] = [];

const exactQuantile = (q: number) => {
  const s = [...window].sort((a, b) => a - b);
  return s[Math.max(1, Math.ceil(q * s.length)) - 1]!;
};

function push(v: number) {
  hist.record(v);
  window.push(v);
  if (window.length > CAPACITY) window.shift();
}

function report(label: string) {
  const p90e = hist.p90, p99e = hist.p99;
  const p90x = exactQuantile(0.9), p99x = exactQuantile(0.99);
  const err90 = (Math.abs(p90e - p90x) / p90x) * 100;
  const err99 = (Math.abs(p99e - p99x) / p99x) * 100;
  console.log(
    `${label.padEnd(28)} P90 est=${p90e.toFixed(2).padStart(9)}ms exact=${p90x.toFixed(2).padStart(9)}ms err=${err90.toFixed(3)}%` +
    ` | P99 est=${p99e.toFixed(2).padStart(10)}ms exact=${p99x.toFixed(2).padStart(10)}ms err=${err99.toFixed(3)}%`,
  );
}

console.log(`窗口容量=${CAPACITY} 桶总数=${hist.buckets} 理论相对误差界限=${(hist.maxRelativeError * 100).toFixed(2)}%\n`);

for (let i = 1; i <= 1000; i++) push(baseline());
report("基线 1000 样本（窗口已满）");

for (let i = 0; i < 40; i++) push(30_000); // 极端毛刺：30s
report("压入 40 个 30s 极端毛刺");

for (let i = 1; i <= 1000; i++) {
  push(baseline());
  if (i === 200 || i === 500) report(`毛刺滑出中（再压入 ${i} 个基线）`);
}
report("毛刺全部滑出（再压入 1000 个）");
