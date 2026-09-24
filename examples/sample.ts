import { BracketAnalyzer, type BracketPair } from '../src/index.ts';

function fmtPair(a: BracketAnalyzer, p: BracketPair): string {
  const s = a.positionAt(p.open);
  const e = a.positionAt(p.close);
  return `  '${p.openChar}' ${s.line}:${s.character}  ->  '${p.closeChar}' ${e.line}:${e.character}  (offsets ${p.open}..${p.close})`;
}

function report(label: string, a: BracketAnalyzer): void {
  console.log(`\n== ${label} ==`);
  console.log('pairs:');
  for (const p of a.getPairs()) console.log(fmtPair(a, p));
  const ds = a.getDiagnostics();
  console.log(ds.length === 0 ? 'diagnostics: (none)' : 'diagnostics:');
  for (const d of ds) {
    const s = a.positionAt(d.start);
    console.log(`  [${d.code}] ${s.line}:${s.character} offsets ${d.start}..${d.end} - ${d.message}`);
  }
}

const source = [
  'fn main() {',
  '  let msg = "brackets ( ) [ ] { } inside strings are ignored";',
  '  // a line comment with ((( unbalanced brackets',
  '  /* a block comment with }}} brackets */',
  '  if (ok) {',
  '    call(msg, [1, 2]);',
  '  }',
  '}',
].join('\n');

console.log('source:\n' + source);

const analyzer = new BracketAnalyzer(source);
report('initial analysis', analyzer);

// Query: minimal enclosing pair of the `1` inside the array literal.
const atOne = source.indexOf('1, 2');
const pair = analyzer.enclosingPair(atOne);
console.log(`\nminimal enclosing pair of offset ${atOne} ('1'):`);
console.log(pair ? fmtPair(analyzer, pair) : '  (none)');

// Edit 1: insert a ')' inside the string literal -> outer structure unchanged.
const inString = source.indexOf('inside strings');
analyzer.applyEdit(inString, inString, ')');
report("after inserting ')' inside the string", analyzer);

// Edit 2: delete the '{' of `if (ok) {` -> subsequent mismatches update.
const brace = analyzer.getText().indexOf('if (ok) {') + 'if (ok) '.length;
analyzer.applyEdit(brace, brace + 1, '');
report("after deleting the '{' of `if (ok)`", analyzer);

// CRLF + emoji: offsets stay in UTF-16 and never split surrogate pairs.
const crlf = new BracketAnalyzer('\u{1F600} fn() {\r\n  go();\r\n}');
report('CRLF text starting with an emoji (UTF-16 offsets)', crlf);
