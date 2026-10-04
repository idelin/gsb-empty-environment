// CLI: check equivalence of two expressions at a given bit width.
//
// Usage:
//   node src/cli.ts --width 8 [--engine auto|symbolic|exhaustive] \
//        [--budget-conflicts N] [--budget-props N] '<exprA>' '<exprB>'
//
// Exit codes: 0 equal, 1 not-equal, 2 unknown, 3 usage/parse error.

import { exprToString } from './ast.ts';
import { checkEquivalence } from './checker.ts';
import { parse, ParseError } from './parser.ts';
import { formatResult } from './result.ts';

function usage(): never {
  console.error(
    'usage: node src/cli.ts --width N [--engine auto|symbolic|exhaustive] ' +
    '[--budget-conflicts N] [--budget-props N] <exprA> <exprB>',
  );
  process.exit(3);
}

const args = process.argv.slice(2);
let width: number | undefined;
let engine: 'auto' | 'symbolic' | 'exhaustive' = 'auto';
let maxConflicts: number | undefined;
let maxPropagations: number | undefined;
const exprs: string[] = [];

for (let i = 0; i < args.length; i++) {
  const arg = args[i];
  if (arg === '--width') width = Number(args[++i]);
  else if (arg === '--engine') engine = args[++i] as typeof engine;
  else if (arg === '--budget-conflicts') maxConflicts = Number(args[++i]);
  else if (arg === '--budget-props') maxPropagations = Number(args[++i]);
  else if (arg.startsWith('--')) usage();
  else exprs.push(arg);
}

if (width === undefined || !Number.isInteger(width) || width < 1) usage();
if (!['auto', 'symbolic', 'exhaustive'].includes(engine)) usage();
if (exprs.length !== 2) usage();

try {
  const a = parse(exprs[0]);
  const b = parse(exprs[1]);
  const result = checkEquivalence(a, b, {
    width,
    engine,
    budget: {
      ...(maxConflicts !== undefined ? { maxConflicts } : {}),
      ...(maxPropagations !== undefined ? { maxPropagations } : {}),
    },
  });
  console.log(`width=${width} engine=${engine}`);
  console.log(`  A = ${exprToString(a)}`);
  console.log(`  B = ${exprToString(b)}`);
  console.log(`  => ${formatResult(result)}`);
  process.exit(result.status === 'equal' ? 0 : result.status === 'not-equal' ? 1 : 2);
} catch (err) {
  if (err instanceof ParseError) {
    console.error(`parse error: ${err.message}`);
    process.exit(3);
  }
  throw err;
}
