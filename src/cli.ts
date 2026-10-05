/** Demo CLI: node src/cli.ts "1 + * f(2,]" [budget] */

import { formatEdits, repair, tokensToSource } from "./repair.ts";
import { tokenize } from "./tokens.ts";

const source = process.argv[2];
if (source === undefined) {
  console.error('usage: node src/cli.ts "<expression>" [budget]');
  process.exit(1);
}
const budget = process.argv[3] === undefined ? undefined : Number(process.argv[3]);
const result = repair(source, budget === undefined ? {} : { budget });

if (result.status === "budget-exceeded") {
  console.log(`budget exceeded after ${result.expansions} expansions`);
  console.log(`optimal cost is between ${result.lowerBound} and ${result.upperBound}`);
} else {
  console.log(`cost: ${result.cost} (${result.expansions} expansions)`);
  for (const line of formatEdits(tokenize(source), result.edits)) console.log(`  ${line}`);
  console.log(`repaired: ${tokensToSource(result.repairedTokens)}`);
}
