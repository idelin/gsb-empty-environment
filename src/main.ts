import { DEMO_METRICS, demoDocument, smallDocument } from './fixtures.ts';
import { DEFAULT_PAGE_CONFIG, attributeFootnotes, buildAtoms, paginate } from './paginate.ts';
import {
  applyCorrection,
  paginateToFixedPoint,
  textMetricsOracle,
} from './fixedpoint.ts';
import { buildCertificate, formatCertificate } from './certificate.ts';
import { exhaustiveMinCost } from './exhaustive.ts';

const config = { ...DEFAULT_PAGE_CONFIG, pageCapacity: 120 };
const metrics = DEMO_METRICS;

console.log('=== 1. Fixed-point pagination of the demo document ===');
const doc = demoDocument();
const fp = paginateToFixedPoint(doc, metrics, config, textMetricsOracle(doc, metrics, 100));
if (!fp.ok) {
  console.log(`UNSOLVABLE: ${fp.reason}`);
  process.exit(1);
}
for (const page of fp.layout.pages) {
  const body = page.atoms.map((a) => `${a.blockId}:${a.kind}`).join(' ');
  const fn = page.footnotes.map((f) => `${f.id}(h=${f.height})`).join(',') || '-';
  console.log(
    `page ${page.index}: [${body}] body=${page.bodyHeight} footnotes=${fn} used=${page.used} slack=${page.slack}`,
  );
}
console.log(
  formatCertificate(
    buildCertificate(doc.id, fp.layout, config, {
      fixedPoint: { converged: true, iterations: fp.iterations },
    }),
  ),
);

console.log('\n=== 2. Local content correction: fn1 grows, body text cascades ===');
const corrected = applyCorrection(doc, {
  footnoteText: { fn1: 'first note now much longer than before and wrapping' },
});
const fp2 = paginateToFixedPoint(
  corrected,
  metrics,
  config,
  textMetricsOracle(corrected, metrics, 100),
);
if (!fp2.ok) {
  console.log(`UNSOLVABLE: ${fp2.reason}`);
  process.exit(1);
}
const before = fp.layout.pages.map((p) => p.atoms.length).join('/');
const after = fp2.layout.pages.map((p) => p.atoms.length).join('/');
console.log(`atoms per page before correction: ${before}`);
console.log(`atoms per page after correction:  ${after}`);
console.log(
  formatCertificate(
    buildCertificate(corrected.id, fp2.layout, config, {
      fixedPoint: { converged: true, iterations: fp2.iterations },
    }),
  ),
);

console.log('\n=== 3. Exhaustive verification on the small document ===');
const small = smallDocument();
const smallConfig = { ...DEFAULT_PAGE_CONFIG, pageCapacity: 50 };
const smallLayout = paginate(small, metrics, smallConfig, { fn1: 10 });
const atoms = buildAtoms(small, metrics).atoms;
const exhaustive = exhaustiveMinCost(atoms, attributeFootnotes(atoms), { fn1: 10 }, smallConfig);
const optimality = exhaustive
  ? {
      verified: exhaustive.minCost === smallLayout.cost,
      layoutsEnumerated: exhaustive.layouts,
      dpCost: smallLayout.cost,
      exhaustiveMinCost: exhaustive.minCost,
    }
  : undefined;
console.log(
  formatCertificate(buildCertificate(small.id, smallLayout, smallConfig, { optimality })),
);
