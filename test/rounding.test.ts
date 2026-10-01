import { test } from "node:test";
import assert from "node:assert/strict";
import { roundQuotient } from "../src/rounding.ts";

test("roundQuotient: floor/ceil on non-ties", () => {
  assert.equal(roundQuotient(7n, 2n, "floor"), 3n);
  assert.equal(roundQuotient(7n, 2n, "ceil"), 4n);
  assert.equal(roundQuotient(6n, 3n, "floor"), 2n);
  assert.equal(roundQuotient(6n, 3n, "ceil"), 2n, "exact value: ceil == floor");
});

test("roundQuotient: exact midpoints, halfUp vs halfEven", () => {
  // 5/2 = 2.5
  assert.equal(roundQuotient(5n, 2n, "halfUp"), 3n);
  assert.equal(roundQuotient(5n, 2n, "halfEven"), 2n, "2.5 -> 2 (even)");
  // 15/10 = 1.5
  assert.equal(roundQuotient(15n, 10n, "halfUp"), 2n);
  assert.equal(roundQuotient(15n, 10n, "halfEven"), 2n, "1.5 -> 2 (even)");
  // 25/10 = 2.5
  assert.equal(roundQuotient(25n, 10n, "halfUp"), 3n);
  assert.equal(roundQuotient(25n, 10n, "halfEven"), 2n, "2.5 -> 2 (even)");
  // 35/10 = 3.5
  assert.equal(roundQuotient(35n, 10n, "halfUp"), 4n);
  assert.equal(roundQuotient(35n, 10n, "halfEven"), 4n, "3.5 -> 4 (even)");
  // 45/10 = 4.5
  assert.equal(roundQuotient(45n, 10n, "halfEven"), 4n, "4.5 -> 4 (even)");
});

test("roundQuotient: nearest on non-ties agrees across half modes", () => {
  for (let num = 0n; num < 500n; num++) {
    for (let den = 1n; den <= 37n; den++) {
      const up = roundQuotient(num, den, "halfUp");
      const even = roundQuotient(num, den, "halfEven");
      const r = num % den;
      if (2n * r !== den) assert.equal(up, even, `${num}/${den}`);
      // nearest result is always one of the two bracketing integers
      const q = num / den;
      assert.ok(up === q || up === q + 1n);
      assert.ok(even === q || even === q + 1n);
    }
  }
});

test("roundQuotient: rejects bad inputs", () => {
  assert.throws(() => roundQuotient(1n, 0n, "floor"), RangeError);
  assert.throws(() => roundQuotient(1n, -2n, "floor"), RangeError);
  assert.throws(() => roundQuotient(-1n, 2n, "floor"), RangeError);
});
