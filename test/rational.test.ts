import { Rational } from "../src/rational";
import { eq, ok, test } from "./harness";

test("rational: normalization and equality", () => {
  ok(Rational.of(2, 4).equals(Rational.of(1, 2)), "2/4 reduces to 1/2");
  ok(Rational.of(-3, -6).equals(Rational.of(1, 2)), "signs normalize");
  ok(Rational.of(5, -10).equals(Rational.of(-1, 2)), "sign moves to numerator");
  ok(Rational.of(0, 7).isZero(), "0/7 is zero");
  eq(Rational.of(6, 4).toString(), "3/2", "toString shows lowest terms");
});

test("rational: arithmetic is exact", () => {
  const a = Rational.of(1, 3);
  const b = Rational.of(1, 6);
  ok(a.add(b).equals(Rational.of(1, 2)), "1/3 + 1/6 = 1/2");
  ok(a.sub(b).equals(Rational.of(1, 6)), "1/3 - 1/6 = 1/6");
  ok(a.mul(b).equals(Rational.of(1, 18)), "1/3 * 1/6 = 1/18");
  ok(a.div(b).equals(Rational.of(2)), "1/3 / 1/6 = 2");
  ok(a.neg().add(a).isZero(), "a + (-a) = 0");
  // (1/3 + 1/7) * 21 == 10 exactly, no float error
  ok(
    Rational.of(1, 3).add(Rational.of(1, 7)).mul(Rational.of(21)).equals(Rational.of(10)),
    "exact over denominators 3 and 7",
  );
});

test("rational: comparison", () => {
  ok(Rational.of(1, 3).compare(Rational.of(1, 2)) < 0, "1/3 < 1/2");
  ok(Rational.of(2, 4).compare(Rational.of(1, 2)) === 0, "2/4 == 1/2");
  ok(Rational.of(-1, 2).compare(Rational.ZERO) < 0, "-1/2 < 0");
});
