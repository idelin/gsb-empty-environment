import { LanguageService } from '../src/index.ts';

function showDiagnostics(svc: LanguageService): void {
  const diags = svc.getDiagnostics();
  if (diags.length === 0) {
    console.log('  diagnostics: (none)');
    return;
  }
  console.log('  diagnostics:');
  for (const d of diags) {
    const pos = svc.positionAt(d.start);
    console.log(`    [${d.code}] offset ${d.start} (line ${pos.line + 1}, col ${pos.character + 1}): ${d.message}`);
  }
}

function showPairs(svc: LanguageService): void {
  console.log('  pairs (open -> close, depth):');
  for (const p of svc.getPairs()) {
    console.log(`    ${p.openChar} @${p.open}  ->  ${p.closeChar} @${p.close}  (depth ${p.depth})`);
  }
}

// ---------------------------------------------------------------- 1. parse
console.log('=== 1. Parse a small program (strings/comments hold no brackets) ===');
const source = [
  'fn main() {',
  '  let s = "brackets (inside) strings [don\'t] count";',
  '  // a comment with ( parens [ galore',
  '  /* block { comment } */',
  '  let x = (1 + [2, 3]);',
  '}',
].join('\n');
const svc = new LanguageService(source);
console.log(source);
showPairs(svc);
showDiagnostics(svc);

// -------------------------------------------------- 2. minimal enclosing pair
console.log('\n=== 2. Minimal enclosing pair query ===');
const haystack = '(a {b} [c] d)';
const svc2 = new LanguageService(haystack);
console.log(haystack);
for (const offset of [4, 8, 11, 6, 0]) {
  const pair = svc2.enclosingPair(offset);
  console.log(
    `  offset ${String(offset).padStart(2)} (${JSON.stringify(haystack[offset] ?? '<eof>')}): ` +
      (pair ? `enclosed by ${pair.openChar}@${pair.open} ... ${pair.closeChar}@${pair.close}` : 'no enclosing pair'),
  );
}

// --------------------------------------- 3. local edit: ")" inside a string
console.log('\n=== 3. Insert ")" inside a string: outer structure unchanged ===');
const svc3 = new LanguageService('f("x")');
console.log(`  before: ${svc3.text}  pairs=${JSON.stringify(svc3.getPairs().map((p) => [p.open, p.close]))}`);
svc3.applyEdit(4, 4, ')');
console.log(`  after:  ${svc3.text}  pairs=${JSON.stringify(svc3.getPairs().map((p) => [p.open, p.close]))}`);
console.log(`  diagnostics: ${svc3.getDiagnostics().length} (inserted ")" matches nothing: matchAt(4)=${svc3.matchAt(4)})`);

// --------------------------------- 4. local edit: delete a middle "("
console.log('\n=== 4. Delete a middle "(": downstream mismatches update ===');
const svc4 = new LanguageService('( [ ) ]');
console.log(`  before: ${svc4.text}`);
showDiagnostics(svc4);
svc4.applyEdit(2, 4, '');
console.log(`  after:  ${svc4.text}`);
showDiagnostics(svc4);

// ------------------------------------------------------------- 5. CRLF
console.log('\n=== 5. CRLF line endings ===');
const svc5 = new LanguageService('fn() {\r\n  (1)\r\n}\r\n');
const open = svc5.offsetAt({ line: 1, character: 2 });
const pair5 = svc5.pairAt(open)!;
console.log(`  "(" at offset ${open} matches ")" at offset ${pair5.close}`);
console.log(`  position of ")": ${JSON.stringify(svc5.positionAt(pair5.close))}`);

// ------------------------------------------------- 6. UTF-16 surrogate pairs
console.log('\n=== 6. UTF-16 offsets and surrogate pairs ===');
const svc6 = new LanguageService('😀(x)');
console.log(`  text "😀(x)" has length ${svc6.text.length} UTF-16 code units`);
console.log(`  pair: ${JSON.stringify(svc6.getPairs().map((p) => [p.open, p.close]))}`);
try {
  svc6.applyEdit(1, 1, '!');
} catch (err) {
  console.log(`  edit splitting the emoji rejected: ${(err as Error).message}`);
}

// ------------------------------------------------------------ 7. depth 10000
console.log('\n=== 7. Depth 10000 (iterative, no recursion) ===');
const depth = 10_000;
const deep = new LanguageService('('.repeat(depth) + 'x' + ')'.repeat(depth));
const t0 = performance.now();
const inner = deep.enclosingPair(depth)!;
const t1 = performance.now();
console.log(`  pairs: ${deep.getPairs().length}, diagnostics: ${deep.getDiagnostics().length}`);
console.log(`  innermost pair around the middle: (${inner.open}, ${inner.close}) at depth ${inner.depth}`);
console.log(`  matchAt(0) = ${deep.matchAt(0)}  (enclosing-pair query took ${(t1 - t0).toFixed(2)} ms)`);
