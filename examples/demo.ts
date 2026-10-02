import { SlidingWindowHistogram } from '../src/histogram';
import { ExactWindow } from '../src/exact';
import { mulberry32, lognormal } from '../src/random';

/**
 * Simulates 6000 requests of long-tail latency with an extreme spike burst
 * in the middle, and prints streaming P90/P99 from the sliding-window
 * histogram side by side with the exact quantiles of a full-retention
 * window. Watch P99 fall back once the spikes slide out of the window.
 */

const CAPACITY = 1000;
const STEPS = 6000;
const SPIKE_START = 2000;
const SPIKE_END = 2050;

const hist = new SlidingWindowHistogram({ capacity: CAPACITY });
const exact = new ExactWindow(CAPACITY);
const rand = mulberry32(20261002);

console.log(
  'step | window | hist P90 | exact P90 | err% | hist P99 | exact P99 | err%'
);
console.log('-----+--------+----------+-----------+------+----------+-----------+-----');

for (let i = 0; i < STEPS; i++) {
  let v: number;
  if (i >= SPIKE_START && i < SPIKE_END) {
    v = 60000 + rand() * 1000; // extreme glitch: ~60s
  } else {
    v = lognormal(rand, 30, 0.9); // baseline long-tail traffic, median ~30ms
  }
  hist.record(v);
  exact.record(v);

  const interesting =
    i % 500 === 0 ||
    i === SPIKE_START ||
    i === SPIKE_END - 1 ||
    i === SPIKE_END + CAPACITY - 50 ||
    i === SPIKE_END + CAPACITY ||
    i === STEPS - 1;
  if (interesting && hist.length > 0) {
    const hp90 = hist.p90();
    const ep90 = exact.p90();
    const hp99 = hist.p99();
    const ep99 = exact.p99();
    const e90 = ((Math.abs(hp90 - ep90) / ep90) * 100).toFixed(3);
    const e99 = ((Math.abs(hp99 - ep99) / ep99) * 100).toFixed(3);
    console.log(
      pad(i, 4) + ' | ' + pad(hist.length, 6) + ' | ' +
      pad(hp90.toFixed(2), 8) + ' | ' + pad(ep90.toFixed(2), 9) + ' | ' +
      pad(e90, 4) + ' | ' + pad(hp99.toFixed(2), 8) + ' | ' +
      pad(ep99.toFixed(2), 9) + ' | ' + pad(e99, 4)
    );
  }
}

function pad(x: any, n: number): string {
  const s = String(x);
  return s.length >= n ? s : new Array(n - s.length + 1).join(' ') + s;
}
