/** Demo CLI: `node src/main.ts "f(1+2,]"` */

import { lex } from './tokens.ts';
import type { Token } from './tokens.ts';
import { repair } from './repair.ts';
import { formatEdit } from './edits.ts';

function render(tokens: Token[]): string {
  return tokens.map((t) => (t.pos === null ? `⟨${t.text}⟩` : t.text)).join(' ');
}

const src = process.argv[2] ?? 'f(1+2,]';
const budget = process.argv[3] !== undefined ? Number(process.argv[3]) : undefined;

const input = lex(src);
console.log(`input:    ${src}`);
console.log(`tokens:   ${input.map((t) => `${t.text}@${t.pos}`).join(' ') || '(empty)'}`);

const result = repair(input, { budget });
if (result.status === 'exceeded') {
  console.log(`exceeded (${result.reason}) after ${result.expansions} expansions`);
  console.log(`bounds:   ${result.lowerBound} <= optimal cost <= ${result.upperBound}`);
} else {
  console.log(`cost:     ${result.cost} (${result.expansions} expansions)`);
  for (const edit of result.edits) console.log(`  ${formatEdit(edit)}`);
  console.log(`repaired: ${render(result.tokens)}`);
  console.log(`positions:${result.tokens.map((t) => (t.pos === null ? '  -' : String(t.pos).padStart(3))).join('')}`);
}
