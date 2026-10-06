const assert: {
  strictEqual(actual: unknown, expected: unknown, message?: string): void;
  ok(value: unknown, message?: string): void;
} = require("node:assert");
import { ConsumeResult, ScrollChain, ScrollLayer } from "../src/scroll-chain";

const EPS = 1e-9;

function approxEqual(a: number, b: number, eps = EPS): boolean {
  return Math.abs(a - b) <= eps * Math.max(1, Math.abs(a), Math.abs(b));
}

/** Per-layer conservation: offered === consumed + remainder + dropped. */
function checkLayerConservation(result: ConsumeResult, label: string): void {
  const accounted = result.consumed + result.remainder + result.dropped;
  assert.ok(
    approxEqual(result.offered, accounted),
    `${label}: offered ${result.offered} != consumed ${result.consumed} + ` +
      `remainder ${result.remainder} + dropped ${result.dropped}`,
  );
  assert.ok(
    result.remainder === 0 || result.dropped === 0,
    `${label}: remainder and dropped are both non-zero`,
  );
}

/** Chain-level conservation: sum(consumed) + sum(dropped) + escaped === delta. */
function checkChainConservation(
  delta: number,
  steps: ConsumeResult[],
  escaped: number,
  label: string,
): void {
  let accounted = escaped;
  for (const step of steps) {
    checkLayerConservation(step, label);
    accounted += step.consumed + step.dropped;
  }
  assert.ok(
    approxEqual(delta, accounted),
    `${label}: chain does not conserve delta ${delta} (accounted ${accounted})`,
  );
}

function scrollAndCheck(chain: ScrollChain, delta: number, label: string) {
  const { steps, escaped } = chain.scroll(delta);
  checkChainConservation(delta, steps, escaped, label);
  return { steps, escaped };
}

const tests: Array<[string, () => void]> = [];
function test(name: string, fn: () => void): void {
  tests.push([name, fn]);
}

test("positive delta fully consumed by inner layer", () => {
  const inner = new ScrollLayer({ min: 0, max: 100, name: "inner" });
  const outer = new ScrollLayer({ min: 0, max: 500, name: "outer" });
  const chain = new ScrollChain([inner, outer]);
  const { escaped } = scrollAndCheck(chain, 40, "inner-only");
  assert.strictEqual(inner.offset, 40);
  assert.strictEqual(outer.offset, 0);
  assert.strictEqual(escaped, 0);
});

test("positive overflow propagates to ancestor, remainder escapes root", () => {
  const inner = new ScrollLayer({ min: 0, max: 100, offset: 80 });
  const outer = new ScrollLayer({ min: 0, max: 50, offset: 40 });
  const chain = new ScrollChain([inner, outer]);
  const { escaped } = scrollAndCheck(chain, 70, "propagate-up");
  assert.strictEqual(inner.offset, 100); // consumed 20
  assert.strictEqual(outer.offset, 50); // consumed 10 of the 50 remainder
  assert.strictEqual(escaped, 40); // 70 - 20 - 10 escapes the root
});

test("negative delta symmetric: underflow propagates upward", () => {
  const inner = new ScrollLayer({ min: 0, max: 100, offset: 15 });
  const outer = new ScrollLayer({ min: -200, max: 0, offset: -10 });
  const chain = new ScrollChain([inner, outer]);
  const { escaped } = scrollAndCheck(chain, -60, "negative");
  assert.strictEqual(inner.offset, 0); // consumed -15
  assert.strictEqual(outer.offset, -55); // consumed -45
  assert.strictEqual(escaped, 0);
  const again = scrollAndCheck(chain, -1000, "negative-escape");
  assert.strictEqual(outer.offset, -200);
  assert.strictEqual(again.escaped, -855); // -1000 + 145 clamped into bounds
});

test("zero-range layer still blocks the chain", () => {
  const inner = new ScrollLayer({ min: 0, max: 100, offset: 100 });
  const blocker = new ScrollLayer({ min: 0, max: 0, blocksChain: true });
  const outer = new ScrollLayer({ min: 0, max: 500 });
  const chain = new ScrollChain([inner, blocker, outer]);
  const { steps, escaped } = scrollAndCheck(chain, 30, "zero-range-block");
  assert.strictEqual(inner.offset, 100);
  assert.strictEqual(blocker.offset, 0);
  assert.strictEqual(outer.offset, 0); // never sees the delta
  assert.strictEqual(escaped, 0);
  const last = steps[steps.length - 1]!;
  assert.strictEqual(last.dropped, 30); // blocker swallowed the overflow
});

test("zero-range non-blocking layer is transparent", () => {
  const zero = new ScrollLayer({ min: 5, max: 5, offset: 5 });
  const outer = new ScrollLayer({ min: 0, max: 500 });
  const chain = new ScrollChain([zero, outer]);
  scrollAndCheck(chain, 25, "zero-range-passthrough");
  assert.strictEqual(zero.offset, 5);
  assert.strictEqual(outer.offset, 25);
});

test("blocking layer with room consumes then drops overflow", () => {
  const inner = new ScrollLayer({ min: 0, max: 100, offset: 90, blocksChain: true });
  const outer = new ScrollLayer({ min: 0, max: 500 });
  const chain = new ScrollChain([inner, outer]);
  const { steps } = scrollAndCheck(chain, 50, "contain");
  assert.strictEqual(inner.offset, 100); // consumed 10
  assert.strictEqual(outer.offset, 0); // 40 dropped, not propagated
  assert.strictEqual(steps[0]!.dropped, 40);
});

test("fractional pixels conserve displacement per layer", () => {
  const inner = new ScrollLayer({ min: 0, max: 10.5, offset: 10.25 });
  const mid = new ScrollLayer({ min: -3.75, max: 3.75, offset: 3.5 });
  const outer = new ScrollLayer({ min: 0, max: 1000 });
  const chain = new ScrollChain([inner, mid, outer]);

  const first = scrollAndCheck(chain, 0.3, "fractional-positive");
  assert.ok(approxEqual(inner.offset, 10.5)); // consumed 0.25
  assert.ok(approxEqual(mid.offset, 3.55)); // consumed the 0.05 remainder
  assert.ok(approxEqual(outer.offset, 0)); // never offered
  assert.ok(approxEqual(first.escaped, 0));

  const second = scrollAndCheck(chain, -12.75, "fractional-negative");
  assert.ok(approxEqual(inner.offset, 0)); // consumed -10.5
  assert.ok(approxEqual(mid.offset, 1.3)); // consumed the -2.25 remainder
  assert.ok(approxEqual(second.escaped, 0));

  const third = scrollAndCheck(chain, 0.7, "fractional-escape");
  assert.ok(approxEqual(inner.offset, 0.7));
  assert.ok(approxEqual(third.escaped, 0));
});

test("dynamic bounds: shrink re-clamps, growth reopens range", () => {
  const inner = new ScrollLayer({ min: 0, max: 100, offset: 90 });
  const outer = new ScrollLayer({ min: 0, max: 500 });
  const chain = new ScrollChain([inner, outer]);

  inner.setBounds(0, 40); // content shrank: offset re-clamped
  assert.strictEqual(inner.offset, 40);

  scrollAndCheck(chain, 30, "dynamic-shrunk");
  assert.strictEqual(inner.offset, 40); // already at max, consumes 0
  assert.strictEqual(outer.offset, 30); // full delta propagated

  inner.setBounds(0, 200); // content grew: range reopens
  scrollAndCheck(chain, -50, "dynamic-regrown");
  assert.strictEqual(inner.offset, 0); // consumes -40 (was 40)
  assert.strictEqual(outer.offset, 20); // consumed -10 of remainder
});

test("dynamic bounds: negative min shifts the clamp window", () => {
  const layer = new ScrollLayer({ min: 0, max: 10, offset: 10 });
  const chain = new ScrollChain([layer]);
  layer.setBounds(-5.5, 4.5);
  assert.strictEqual(layer.offset, 4.5);
  scrollAndCheck(chain, -20, "dynamic-negative-min");
  assert.strictEqual(layer.offset, -5.5);
  const { escaped } = scrollAndCheck(chain, -1, "dynamic-escape");
  assert.strictEqual(escaped, -1);
});

test("zoom preserves the anchor when nothing clamps", () => {
  const inner = new ScrollLayer({ min: 0, max: 10000, offset: 100 });
  const outer = new ScrollLayer({ min: 0, max: 10000, offset: 300 });
  const chain = new ScrollChain([inner, outer]);
  const anchor = 40;
  const factor = 2;
  const pinnedInner = inner.offset + anchor;
  const pinnedOuter = outer.offset + anchor;
  const steps = chain.applyZoom(factor, anchor);
  for (const step of steps) checkLayerConservation(step.result, "zoom-noclamp");
  assert.ok(approxEqual((inner.offset + anchor) / factor, pinnedInner));
  assert.ok(approxEqual((outer.offset + anchor) / factor, pinnedOuter));
});

test("zoom clamp overflow propagates upward like a scroll", () => {
  const inner = new ScrollLayer({ min: 0, max: 150, offset: 100 });
  const outer = new ScrollLayer({ min: 0, max: 1000, offset: 0 });
  const chain = new ScrollChain([inner, outer]);
  // anchor 0: desired inner offset = 200, clamped to 150, 50 pushes outer
  const steps = chain.applyZoom(2, 0);
  for (const step of steps) checkLayerConservation(step.result, "zoom-clamp");
  assert.strictEqual(inner.offset, 150);
  assert.strictEqual(outer.offset, 50);
});

test("zoom overflow respects a blocking layer", () => {
  const inner = new ScrollLayer({ min: 0, max: 150, offset: 100, blocksChain: true });
  const outer = new ScrollLayer({ min: 0, max: 1000, offset: 0 });
  const chain = new ScrollChain([inner, outer]);
  const steps = chain.applyZoom(2, 0);
  for (const step of steps) checkLayerConservation(step.result, "zoom-blocked");
  assert.strictEqual(inner.offset, 150);
  assert.strictEqual(steps[0]!.result.dropped, 50);
  assert.strictEqual(outer.offset, 0); // blocking layer stopped propagation
});

test("zoom out with fractional factor and anchor stays conserved", () => {
  const inner = new ScrollLayer({ min: 0, max: 100, offset: 33.3 });
  const outer = new ScrollLayer({ min: 0, max: 100, offset: 66.6 });
  const chain = new ScrollChain([inner, outer]);
  const anchor = 12.5;
  const factor = 0.75;
  const pinnedInner = inner.offset + anchor;
  const steps = chain.applyZoom(factor, anchor);
  for (const step of steps) checkLayerConservation(step.result, "zoom-out");
  assert.ok(approxEqual((inner.offset + anchor) / factor, pinnedInner));
  assert.ok(approxEqual(outer.offset, (66.6 + anchor) * factor - anchor));
});

let failures = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (error) {
    failures++;
    console.error(`FAIL - ${name}`);
    console.error(error instanceof Error ? error.message : error);
  }
}
console.log(`${tests.length - failures}/${tests.length} tests passed`);
if (failures > 0) process.exit(1);
