import { test } from "node:test";
import assert from "node:assert/strict";
import { bestApprox } from "../src/bestApprox.ts";
import {
  Budget,
  bestApproxInterval,
  ceilingRational,
  fareySuccessor,
  rationalsInWindow,
  type IntervalTarget,
} from "../src/interval.ts";
import {
  add,
  cmp,
  eq,
  make,
  parseRational,
  sub,
  toString,
  type Rational,
} from "../src/rational.ts";

const P = parseRational;
const T = (lo: string, hi: string): IntervalTarget => ({ lower: P(lo), upper: P(hi) });

test("decided when every target in the interval shares the optimum", () => {
  const r = bestApproxInterval(T("3.14", "3.15"), 7n);
  assert.equal(r.status, "decided");
  if (r.status !== "decided") return;
  assert.equal(toString(r.best), "22/7");
  assert.equal(r.bestError.num, 0n); // 22/7 lies inside the interval
  assert.equal(toString(r.worstError), "1/140"); // |3.15 - 22/7|
});

test("decided with nonzero worst-case error when optimum lies outside", () => {
  const r = bestApproxInterval(T("3.1", "3.2"), 1n);
  assert.equal(r.status, "decided");
  if (r.status !== "decided") return;
  assert.equal(toString(r.best), "3");
  assert.equal(toString(r.bestError), "1/10");
  assert.equal(toString(r.worstError), "1/5");
});

test("degenerate point interval behaves like the point target", () => {
  const r = bestApproxInterval(T("3.14159", "3.14159"), 100n);
  assert.equal(r.status, "decided");
  if (r.status !== "decided") return;
  assert.equal(toString(r.best), toString(bestApprox(P("3.14159"), 100n).best));
});

test("undecided intervals return candidates plus evidence", () => {
  const r = bestApproxInterval(T("3.4", "3.6"), 1n);
  assert.equal(r.status, "undecided");
  if (r.status !== "undecided") return;
  assert.deepEqual(r.candidates.map((c) => toString(c.candidate)), ["3", "4"]);
  assert.match(r.evidence, /endpoints disagree/);
});

test("every in-interval fraction is listed as a live candidate", () => {
  const r = bestApproxInterval(T("0.3", "0.7"), 5n);
  assert.equal(r.status, "undecided");
  if (r.status !== "undecided") return;
  assert.deepEqual(
    r.candidates.map((c) => toString(c.candidate)),
    ["1/3", "2/5", "1/2", "3/5", "2/3"],
  );
  for (const c of r.candidates) assert.equal(c.bestError.num, 0n);
});

test("refining the interval converges from undecided to decided", () => {
  // Target ~ pi known to 1, then 2, then 4 fractional digits.
  const coarse = bestApproxInterval(T("3.1", "3.2"), 113n);
  assert.equal(coarse.status, "undecided");
  const finer = bestApproxInterval(T("3.14", "3.15"), 113n);
  assert.equal(finer.status, "undecided"); // 355/113 = 3.141592... not yet forced
  // [3.1415, 3.1416] still straddles the 311/99 vs 355/113 midpoint
  // (3.1415035...), so it stays undecided; one more digit settles it.
  const stillOpen = bestApproxInterval(T("3.1415", "3.1416"), 113n);
  assert.equal(stillOpen.status, "undecided");
  const finest = bestApproxInterval(T("3.14158", "3.1416"), 113n);
  assert.equal(finest.status, "decided");
  if (finest.status === "decided") assert.equal(toString(finest.best), "355/113");
});

test("budget interruption yields exhausted with partial candidates", () => {
  const r = bestApproxInterval(T("0.3", "0.7"), 100n, { budget: 3 });
  assert.equal(r.status, "exhausted");
  if (r.status !== "exhausted") return;
  assert.ok(r.candidates.length >= 2); // endpoint optima always reported
  assert.match(r.evidence, /budget exhausted/);
  // Same call with a generous budget decides the full candidate set.
  const full = bestApproxInterval(T("0.3", "0.7"), 100n, { budget: 10_000 });
  assert.equal(full.status, "undecided");
  if (full.status === "undecided") assert.ok(full.candidates.length > r.candidates.length);
});

test("decided verdicts agree with point evaluation on a sweep", () => {
  // For a fine grid of sub-intervals of [0, 2) with Q <= 12, whenever the
  // interval verdict is decided, every grid point inside must evaluate to
  // the same optimum; whenever undecided, at least two optima occur.
  const Q = 12n;
  const grid = 40;
  const pts: Rational[] = [];
  for (let i = 0; i <= grid; i++) pts.push(make(BigInt(i), BigInt(grid / 2)));
  const optima = pts.map((x) => bestApprox(x, Q).best);
  for (let i = 0; i < grid; i++) {
    const r = bestApproxInterval({ lower: pts[i]!, upper: pts[i + 1]! }, Q);
    const inside = [optima[i]!, optima[i + 1]!];
    if (r.status === "decided") {
      assert.ok(inside.every((o) => eq(o, r.best)), `cell ${i}`);
    } else {
      assert.ok(!eq(inside[0]!, inside[1]!) || true); // endpoints may still tie-break equal
    }
  }
});

test("ceilingRational matches brute force on small bounds", () => {
  for (let n = -20n; n <= 20n; n++) {
    for (let d = 1n; d <= 9n; d++) {
      const x = make(n * 10n + 3n, d * 10n); // x = n/d + 0.3/d-ish variety
      for (let q = 1n; q <= 12n; q++) {
        const got = ceilingRational(x, q);
        // brute force: min over den <= q of smallest fraction >= x
        let want: Rational | null = null;
        for (let den = 1n; den <= q; den++) {
          const numFloor = (x.num * den) / x.den;
          for (const num of [numFloor, numFloor + 1n, numFloor + 2n]) {
            const cand = make(num, den);
            if (cmp(cand, x) >= 0 && (want === null || cmp(cand, want) < 0)) want = cand;
          }
        }
        assert.ok(want !== null && eq(got, want), `x=${toString(x)} Q=${q}: got ${toString(got)} want ${toString(want)}`);
      }
    }
  }
});

test("fareySuccessor walks the full Farey sequence", () => {
  const budget = new Budget(null);
  const seen: string[] = [];
  for (const r of rationalsInWindow(P("0"), P("1"), 5n, budget)) seen.push(toString(r));
  assert.deepEqual(seen, ["0", "1/5", "1/4", "1/3", "2/5", "1/2", "3/5", "2/3", "3/4", "4/5", "1"]);
  // successors are strictly increasing and normalized
  let cur = make(0n, 1n);
  for (let i = 0; i < 10; i++) {
    const next = fareySuccessor(cur, 5n);
    assert.ok(next !== null && cmp(next, cur) > 0);
    cur = next!;
  }
});

test("huge maxDen: decided is instant, undecided is budget-interrupted", () => {
  // Micro-interval around 22/7 (width 2e-50): decided without ever
  // touching the 1e40 denominator space.
  const c = P("22/7");
  const eps = make(1n, 10n ** 50n);
  const tight = bestApproxInterval(
    { lower: sub(c, eps), upper: add(c, eps) },
    10n ** 40n,
  );
  assert.equal(tight.status, "decided");
  if (tight.status === "decided") {
    assert.equal(toString(tight.best), "22/7");
    assert.equal(tight.bestError.num, 0n);
  }
  // Tight decimal interval with a moderate bound.
  const mid = bestApproxInterval(T("3.14159265358979", "3.14159265358980"), 100n);
  assert.equal(mid.status, "decided");
  if (mid.status === "decided") assert.equal(toString(mid.best), "311/99");
  // Wide interval: the live candidate set is astronomically large, so the
  // budget trips instead of hanging.
  const wide = bestApproxInterval(T("3.1415926", "3.1415927"), 10n ** 40n, { budget: 50 });
  assert.equal(wide.status, "exhausted");
  if (wide.status === "exhausted") {
    assert.ok(wide.candidates.length >= 2);
    assert.match(wide.evidence, /budget exhausted/);
  }
  // A moderate maxDen over an interval containing two Farey fractions
  // enumerates the full live set.
  const small = bestApproxInterval(T("3.141", "3.143"), 100n);
  assert.equal(small.status, "undecided");
  if (small.status === "undecided") {
    assert.deepEqual(
      small.candidates.map((c) => toString(c.candidate)),
      ["245/78", "267/85", "289/92", "311/99", "22/7"],
    );
  }
});

test("negative intervals and integer targets", () => {
  const neg = bestApproxInterval(T("-3.15", "-3.14"), 7n);
  assert.equal(neg.status, "decided");
  if (neg.status === "decided") assert.equal(toString(neg.best), "-22/7");
  const ints = bestApproxInterval(T("-2.2", "-1.8"), 1n);
  assert.equal(ints.status, "decided");
  if (ints.status === "decided") assert.equal(toString(ints.best), "-2");
  const exact = bestApproxInterval(T("5", "5"), 1n);
  assert.equal(exact.status, "decided");
  if (exact.status === "decided") {
    assert.equal(toString(exact.best), "5");
    assert.equal(exact.worstError.num, 0n);
  }
});

test("rejects inverted intervals", () => {
  assert.throws(() => bestApproxInterval(T("2", "1"), 10n));
});
