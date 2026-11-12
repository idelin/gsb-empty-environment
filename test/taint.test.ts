import assert from "node:assert/strict";
import test from "node:test";
import {
  CLEAN,
  analyzePerl,
  arrayElementTaint,
  concatTaint,
  hashValueTaint,
  interpolateTaint,
  isAnchoredWholeStringPattern,
  joinTaint,
  regexCaptureTaint,
  substrTaint,
  taintSource,
} from "../index.ts";

function source(expression = "$ARGV[0]") {
  return taintSource(expression, { line: 1, column: 1 });
}

test("low-level propagation functions preserve the source chain", () => {
  const input = source("$ENV{QUERY}");
  const assigned = concatTaint([input, CLEAN], { line: 2, column: 3, label: "$cmd" });
  const interpolated = interpolateTaint([assigned], { line: 3, column: 1 });
  const joined = joinTaint(CLEAN, [interpolated], { line: 4, column: 1 });
  const sliced = substrTaint(joined, { line: 5, column: 1 });
  const hashed = hashValueTaint(sliced, "cmd", { line: 6, column: 1 });
  const indexed = arrayElementTaint(hashed, 0, { line: 7, column: 1 });

  assert.equal(indexed.tainted, true);
  assert.equal(indexed.traces.length, 1);
  assert.equal(indexed.traces[0].origin.expression, "$ENV{QUERY}");
  assert.deepEqual(
    indexed.traces[0].steps.map((step) => step.kind),
    ["concat", "interpolation", "join", "substr", "hash-value", "array-element"],
  );
});

test("only anchored whole-string patterns without .* cleanse captures", () => {
  assert.equal(isAnchoredWholeStringPattern("^(\\w+)$"), true);
  assert.equal(isAnchoredWholeStringPattern("^(\\w+)$", "i"), true);
  assert.equal(isAnchoredWholeStringPattern("(\\w+)"), false);
  assert.equal(isAnchoredWholeStringPattern("^(\\w+)$", "m"), false);
  assert.equal(isAnchoredWholeStringPattern("^(.*)$"), false);
  assert.equal(isAnchoredWholeStringPattern("^(a+).*$"), false);

  const input = source("<STDIN>");
  const clean = regexCaptureTaint(input, "^(\\w+)$", 1);
  const dirty = regexCaptureTaint(input, "(\\w+)", 1);
  assert.equal(clean.tainted, false);
  assert.equal(dirty.tainted, true);
  assert.equal(dirty.traces[0].steps.at(-1)?.kind, "regex-capture-retained");
});

test("unanchored capture keeps taint and reaches system", () => {
  const result = analyzePerl(`
my $input = <STDIN>;
$input =~ /(\\w+)/;
my $value = $1;
system("echo $value");
`);
  assert.equal(result.findings.length, 1);
  assert.equal(result.findings[0].sink, "system");
  assert.equal(result.findings[0].sources[0].expression, "<STDIN>");
  assert.match(result.findings[0].chain[0], /<STDIN>/);
  assert.match(result.findings[0].chain[0], /system/);
});

test("concatenation and interpolation propagate taint into exec", () => {
  const result = analyzePerl(`
my $arg = $ARGV[0];
my $cmd = "run --target=" . $arg;
exec($cmd);
`);
  assert.equal(result.findings.length, 1);
  assert.equal(result.findings[0].sink, "exec");
  assert.match(result.findings[0].chain[0], /\$ARGV\[0\]/);
  assert.match(result.findings[0].chain[0], /concatenation/);
});

test("join and substr preserve taint", () => {
  const joined = analyzePerl(`
my @parts = @ARGV;
my $cmd = join(" ", @parts);
system($cmd);
`);
  assert.equal(joined.findings.length, 1);

  const sliced = analyzePerl(`
my $value = $ARGV[0];
my $part = substr($value, 1, 3);
system($part);
`);
  assert.equal(sliced.findings.length, 1);
  assert.match(sliced.findings[0].chain[0], /substr/);
});

test("hash and array element propagation retain exact values", () => {
  const hash = analyzePerl(`
my $input = <STDIN>;
my %options = (cmd => $input, safe => "fixed");
my $selected = $options{cmd};
system($selected);
`);
  assert.equal(hash.findings.length, 1);
  assert.match(hash.findings[0].chain[0], /hash value/);

  const array = analyzePerl(`
my @values = ($ARGV[0], "safe");
my $selected = $values[0];
exec($selected);
`);
  assert.equal(array.findings.length, 1);
  assert.match(array.findings[0].chain[0], /array element/);
});

test("anchored whitelist capture cleans the value", () => {
  const result = analyzePerl(`
my $input = <STDIN>;
$input =~ /^(\\w+)$/;
my $safe = $1;
system("echo $safe");
`);
  assert.equal(result.findings.length, 0);
});

test(".* disables cleansing even when anchored", () => {
  const result = analyzePerl(`
my $input = <STDIN>;
$input =~ /^(.*)$/;
my $value = $1;
system($value);
`);
  assert.equal(result.findings.length, 1);
});

test("backticks and write-mode open are sinks", () => {
  const backtick = analyzePerl(`
my $input = $ARGV[0];
my $output = \`echo $input\`;
`);
  assert.equal(backtick.findings.length, 1);
  assert.equal(backtick.findings[0].sink, "backtick");

  const writeOpen = analyzePerl(`
my $path = $ARGV[1];
open(FH, ">$path");
`);
  assert.equal(writeOpen.findings.length, 1);
  assert.equal(writeOpen.findings[0].sink, "open");

  const readOpen = analyzePerl(`
my $path = $ARGV[1];
open(FH, "<$path");
`);
  assert.equal(readOpen.findings.length, 0);
});

test("multiple statements on one line are still analyzed", () => {
  const result = analyzePerl(`my $x = <STDIN>; system($x);`);
  assert.equal(result.findings.length, 1);
});

test("array mutation and shift follow array taint", () => {
  const pushed = analyzePerl(`
my @args = ("fixed");
push @args, $ARGV[0];
system($args[1]);
`);
  assert.equal(pushed.findings.length, 1);
  assert.match(pushed.findings[0].chain[0], /array-element/);

  const cleanShift = analyzePerl(`
my @args = ("fixed");
my $value = shift @args;
system($value);
`);
  assert.equal(cleanShift.findings.length, 0);

  const externalShift = analyzePerl(`
my $value = shift;
exec($value);
`);
  assert.equal(externalShift.findings.length, 1);
});

test("three-argument write open reports a tainted path", () => {
  const result = analyzePerl(`
my $path = $ARGV[1];
open my $fh, ">", $path;
`);
  assert.equal(result.findings.length, 1);
  assert.equal(result.findings[0].sink, "open");
});

test("assignment inside a loop condition becomes a source", () => {
  const result = analyzePerl(`
while (my $line = <STDIN>) {
  system("echo " . $line);
}
`);
  assert.equal(result.findings.length, 1);
  assert.equal(result.findings[0].sources[0].expression, "<STDIN>");
});

test("ternary branches merge conservatively and keep capture taint", () => {
  const result = analyzePerl(`
my $input = <STDIN>;
my $value = $input =~ /(\\w+)/ ? $1 : "fallback";
system($value);
`);
  assert.equal(result.findings.length, 1);
  assert.match(result.findings[0].chain[0], /regex-capture-retained/);
});
