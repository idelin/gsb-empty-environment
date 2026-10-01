/**
 * Runnable samples: node --no-warnings examples/demo.ts   (or: npm run demo)
 */
import { convert, formatRational, parseRational } from "../src/convert.ts";
import { FractionExpander, verifyFractionDigits } from "../src/fraction.ts";
import { Rational } from "../src/rational.ts";

function show(title: string, fn: () => void): void {
  console.log(`\n=== ${title} ===`);
  fn();
}

function showReport(r: { report: { [k: string]: unknown } }): void {
  const rp = r.report;
  console.log(
    `    report: divisions=${rp.integerDivisions} fracSteps=${rp.fractionSteps}` +
      ` budget=${rp.budget} used=${rp.budgetUsed} emitted=${rp.digitsEmitted}` +
      ` status=${rp.status} complete=${rp.complete} rounded=${rp.rounded}`,
  );
}

show("big integer: 500-digit decimal -> hex (chunked, workload reported)", () => {
  const big = "9".repeat(500);
  const out = convert(big, { fromBase: 10, base: 16 });
  console.log(`    hex: ${out.text.slice(0, 64)}... (${out.text.length} digits)`);
  showReport(out);
});

show("decimal 0.1 -> binary: repeating cycle, output STOPS", () => {
  const out = convert("0.1", { fromBase: 10, base: 2 });
  console.log(`    0.1 (dec) = ${out.text} (bin)`);
  showReport(out);
  const back = convert(out.text, { fromBase: 2, base: 10 });
  console.log(`    round-trip back: ${back.text} (dec)`);
});

show("1/7 in bases 10 / 2 / 36 / 62", () => {
  const v = new Rational(1n, 7n);
  for (const [base, charset] of [[10, "lower"], [2, "lower"], [36, "lower"], [62, "base62"]] as const) {
    const out = formatRational(v, { base, charset });
    console.log(`    base ${String(base).padStart(2)}: ${out.text}`);
  }
});

show("output budget: incomplete is explicit", () => {
  const out = formatRational(new Rational(1n, 7n), { base: 10, budget: 3 });
  console.log(`    1/7 budget=3 -> ${out.text}  complete=${out.complete} (${out.report.status})`);
  const near = formatRational(new Rational(1n, 7n), { base: 10, budget: 3, rounding: "nearest" });
  console.log(`    1/7 budget=3 rounding=nearest -> ${near.text}  rounded=${near.report.rounded}`);
});

show("pausable output: pause at 13 digits, snapshot, restore, resume", () => {
  const r = new Rational(1n, 97n);
  const ex = new FractionExpander(1n, r.den, 10, "lower");
  ex.run(13);
  ex.pause();
  console.log(`    paused after ${ex.getSteps()} digits: 0.${ex.getDigits()}...`);
  const snap = JSON.stringify(ex.snapshot());
  const ex2 = FractionExpander.restore(JSON.parse(snap), r.den, 10, "lower");
  ex2.resume();
  ex2.run();
  console.log(`    resumed to completion: status=${ex2.getStatus()} cycle=${ex2.getCycle().slice(0, 32)}... (${ex2.getCycle().length} digits)`);
});

show("independent cycle verification (closed form, no re-expansion)", () => {
  const ok = verifyFractionDigits(new Rational(1n, 7n), 10, "lower", "", "142857");
  console.log(`    1/7 ~ 0.(142857): valid=${ok.valid} minimal=${ok.minimalCycle} canonical=${ok.canonicalStart}`);
  const bad = verifyFractionDigits(new Rational(1n, 7n), 10, "lower", "", "142858");
  console.log(`    1/7 ~ 0.(142858): valid=${bad.valid} reason="${bad.reason}"`);
});

show("negative zero rules", () => {
  for (const s of ["-0", "-0.0", "-0.(0)"]) {
    console.log(`    parse("${s}") -> ${parseRational(s, 10).toString()} -> renders "${formatRational(parseRational(s, 10), { base: 10 }).text}"`);
  }
});

show("invalid digit reports its position", () => {
  try {
    parseRational("12.3a5", 10);
  } catch (e) {
    console.log(`    parse("12.3a5") -> ${(e as Error).message}`);
  }
});

show("charset families are case-sensitive", () => {
  console.log(`    255 base16 lower: ${formatRational(new Rational(255n), { base: 16, charset: "lower" }).text}`);
  console.log(`    255 base16 upper: ${formatRational(new Rational(255n), { base: 16, charset: "upper" }).text}`);
  console.log(`    255 base62:       ${formatRational(new Rational(255n), { base: 62, charset: "base62" }).text}`);
});
