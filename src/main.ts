import type { ExactPremult } from './color.ts';
import { quantizePremult8 } from './color.ts';
import type { Layer } from './compose.ts';
import { composeExact, composeGrouped, composeOneShot } from './compose.ts';
import { findMinimalCounterexample, stackToLayers } from './search.ts';

const fmtExact = (c: ExactPremult): string =>
  `(r=${c.r.toString()}, g=${c.g.toString()}, b=${c.b.toString()}, a=${c.a.toString()})`;

const fmtLayer = (l: Layer): string => {
  const { r, g, b, a } = l.color;
  return `${l.format === 'straight' ? 'straight  ' : 'premultiplied'} (${r}, ${g}, ${b}, ${a})`;
};

const found = findMinimalCounterexample();
if (!found) {
  console.log('No counterexample found.');
  process.exit(0);
}

const { stack, grouping, bound, stacksBelowBound, stacksAtBound } = found;
const layers = stackToLayers(stack);

console.log('=== Minimal counterexample: one-shot vs group-then-quantize ===\n');
console.log('Layers (bottom to top), gray premultiplied 8-bit:');
layers.forEach((l, i) => console.log(`  L${i + 1}: ${fmtLayer(l)}`));
console.log(`Grouping: [${grouping.join(', ')}] (sizes of contiguous groups, bottom first)\n`);

console.log('Exact rational intermediates (0..255 scale):');
const midOverBottom = composeExact(layers.slice(0, 2));
const topOverMid = composeExact(layers.slice(1, 3));
const all = composeExact(layers);
console.log(`  L2 over L1 = ${fmtExact(midOverBottom)}  ->  quantize ${JSON.stringify(quantizePremult8(midOverBottom))}`);
console.log(`  L3 over L2 = ${fmtExact(topOverMid)}  ->  quantize ${JSON.stringify(quantizePremult8(topOverMid))}`);
console.log(`  L3 over L2 over L1 (exact) = ${fmtExact(all)}\n`);

const oneShot = composeOneShot(layers);
const grouped = composeGrouped(layers, grouping);
console.log('One-shot (compose all exactly, quantize once):');
console.log(`  premultiplied: ${JSON.stringify(oneShot.premultiplied)}  straight: ${JSON.stringify(oneShot.straight)}`);
console.log(`Grouped [${grouping.join(', ')}] (quantize each group to 8-bit before continuing):`);
console.log(`  premultiplied: ${JSON.stringify(grouped.premultiplied)}  straight: ${JSON.stringify(grouped.straight)}\n`);

console.log('Minimality:');
console.log('  - 2 layers can never differ: grouping [2] is one-shot, and [1,1]');
console.log('    quantizes 8-bit inputs to themselves. So 3 layers is minimal.');
console.log(`  - Exhaustive check of all gray 3-stacks with every byte <= ${bound - 1}: no difference (${stacksBelowBound} stacks).`);
console.log(`  - First difference appears at max byte value = ${bound} (${stacksAtBound} stacks checked at this bound).`);
console.log('  - RGB channels are independent under "over", so gray minimality implies RGBA minimality.');
console.log(`  - Among max=${bound} counterexamples this one also minimizes the byte sum, then lexicographic order.`);

const premultOnly = findMinimalCounterexample(255, false);
if (premultOnly) {
  console.log('\nIf only premultiplied output is compared (no un-premultiplication):');
  console.log(`  - First divergence appears at max byte value = ${premultOnly.bound}.`);
  console.log(`  - Example stack (bottom to top): ${JSON.stringify(premultOnly.stack)}`);
  console.log(`  - One-shot ${JSON.stringify(premultOnly.oneShot.premultiplied)} vs grouped ${JSON.stringify(premultOnly.grouped.premultiplied)}`);
}
