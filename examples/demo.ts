// Runnable tour of the style engine: computed styles with per-property source
// chains, scoped variables, conditional selectors, variable cycles with
// fallback paths, incremental updates with atomic publish, and fixture-driven
// layout. Run with: npm run demo   (Node >= 23.6)

import { readFileSync } from 'node:fs';
import { buildDocument, flatten } from '../src/engine/model.ts';
import { StyleEngine } from '../src/engine/engine.ts';
import { computeAllStyles } from '../src/engine/reference.ts';
import { layoutDocument } from '../src/layout/layout.ts';
import type { GlyphMetrics } from '../src/layout/layout.ts';
import type { ComputedValue } from '../src/engine/computed.ts';

const CSS = `
.app { --accent: rebeccapurple; --gap: 8; color: var(--accent); font-size: 16; line-height: 1; }
.app.theme-dark { --accent: gold; }
.card { --accent: teal; padding: var(--gap); margin: 2; }
.title { color: var(--accent); font-size: 24; }
.desc { color: var(--missing, var(--accent)); }
.loop { --a: var(--b); --b: var(--a); color: var(--a, crimson); width: var(--a); }
.desc:hover { color: hotpink; }
p.desc { margin: 4; }
.desc { margin: 1; }
`;

const root = buildDocument({
  tag: 'div', classes: ['app'], children: [
    { tag: 'h1', classes: ['title'], text: 'Hello' },
    {
      tag: 'section', classes: ['card'], children: [
        { tag: 'p', classes: ['desc'], text: 'ab ab' },
        { tag: 'p', classes: ['desc', 'loop'], text: 'ab' },
      ],
    },
  ],
});
const nodes = flatten(root);
const [app, title, card, desc, loop] = nodes;

function show(label: string, cv: ComputedValue | undefined): void {
  if (!cv) return;
  console.log(`  ${label} = ${JSON.stringify(cv.value)}  (source: ${JSON.stringify(cv.source)})`);
  for (const c of cv.chain) {
    console.log(`    [${c.outcome}] ${c.selectorText} { ${cv.prop}: ${c.raw} }  spec=${c.specificity.join(',')}${c.inline ? ' inline' : ''} rule#${c.ruleId}`);
  }
  for (const v of cv.vars) {
    const extra = v.reason ? ` reason=${v.reason}${v.cyclePath ? ` cycle=${v.cyclePath.join(' -> ')}` : ''}` : '';
    console.log(`    var(${v.name}) -> ${v.status}${v.value !== undefined ? ` = ${JSON.stringify(v.value)}` : ''}${extra}`);
  }
}

const engine = new StyleEngine(root, CSS);
console.log('== 1. Computed styles with per-property source chains ==');
let snap = engine.getSnapshot();
console.log(`title#${title.id} color:`);
show('color', snap.get(title.id, 'color'));
console.log(`desc#${desc.id} color (undefined var -> fallback -> scoped --accent from .card):`);
show('color', snap.get(desc.id, 'color'));
console.log(`desc#${desc.id} margin (same-specificity source order):`);
show('margin', snap.get(desc.id, 'margin'));
console.log(`loop#${loop.id} color (variable cycle -> fallback) and width (cycle, no fallback -> unset):`);
show('color', snap.get(loop.id, 'color'));
show('width', snap.get(loop.id, 'width'));
console.log(`loop#${loop.id} --a (cyclic custom property is guaranteed-invalid):`);
show('--a', snap.get(loop.id, '--a'));

console.log('\n== 2. Incremental update: add theme-dark class to the root ==');
const before = engine.getSnapshot();
const report = engine.setClass(app.id, 'theme-dark', true);
snap = engine.getSnapshot();
console.log(`  version ${before.version} -> ${snap.version}; recomputed ${report.evaluated.length} entries:`);
console.log(`  ${report.evaluated.join(', ')}`);
console.log(`  title color: ${before.get(title.id, 'color')!.value} -> ${snap.get(title.id, 'color')!.value}`);
console.log(`  desc color (scoped .card --accent shadows root): ${snap.get(desc.id, 'color')!.value}`);
console.log(`  old snapshot still reads the old value: ${before.get(title.id, 'color')!.value} (version ${before.version})`);

console.log('\n== 3. Incremental update: edit a variable in the stylesheet ==');
const report2 = engine.updateStylesheet(CSS.replace('--gap: 8', '--gap: 20'));
snap = engine.getSnapshot();
console.log(`  recomputed ${report2.evaluated.length} entries: ${report2.evaluated.join(', ')}`);
console.log(`  card padding: ${snap.get(card.id, 'padding')!.value}`);

console.log('\n== 4. Conditional selector: toggle :hover on desc ==');
engine.toggleState(desc.id, 'hover');
snap = engine.getSnapshot();
console.log(`  desc color while hovered: ${snap.get(desc.id, 'color')!.value}`);

console.log('\n== 5. Cross-check against the independent full interpreter ==');
const ref = computeAllStyles(nodes, CSS.replace('--gap: 8', '--gap: 20'), new Map());
const current = engine.getSnapshot();
console.log(`  engine entries: ${current.store.size}, reference entries: ${ref.size}`);
console.log(`  deep-equal: ${JSON.stringify([...current.store].sort()) === JSON.stringify([...ref].sort())}`);

console.log('\n== 6. Fixture-driven layout (glyph metrics, no browser) ==');
const metrics = JSON.parse(readFileSync(new URL('../fixtures/glyphs.json', import.meta.url), 'utf8')) as GlyphMetrics;
const box = layoutDocument(root, engine.getSnapshot(), metrics, 80);
const printBox = (b: typeof box, indent: string): void => {
  const lines = b.lines.length > 0 ? ` lines=${JSON.stringify(b.lines.map((l) => l.text))}` : '';
  console.log(`${indent}node#${b.nodeId} x=${b.x} y=${b.y} w=${b.width} h=${b.height}${lines}`);
  for (const c of b.children) printBox(c, `${indent}  `);
};
printBox(box, '  ');
