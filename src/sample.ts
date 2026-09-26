import { paginate } from "./paginate.ts";
import { buildCertificate } from "./certificate.ts";
import { resolveFixpoint } from "./fixpoint.ts";
import { applyEdit, applyFootnoteEdit, repaginateLocal } from "./update.ts";
import { fixtureMetrics, para, sampleConfig, sampleDocument } from "./fixtures.ts";
import { validateLayout } from "./validate.ts";

const metrics = fixtureMetrics();
const cfg = sampleConfig();
const doc = sampleDocument();

console.log("== Pagination ==");
const result = paginate(doc, metrics, cfg);
if (!result.feasible) {
  console.log(`INFEASIBLE: ${result.error}`);
  process.exit(1);
}
for (const page of result.pages) {
  const kinds = [];
  for (let k = page.start; k < page.end; k++) {
    const it = result.items[k];
    kinds.push(it.type === "line" ? `line(${it.nodeId}#${it.lineIndex})` : `${it.type}(${it.nodeId})`);
  }
  console.log(
    `page ${page.index}: body=${page.bodyUsed} footnotes=${page.footnoteHeight}` +
      ` ids=[${page.footnoteIds.join(",")}] slack=${cfg.pageHeight - page.bodyUsed - page.footnoteHeight}`,
  );
  console.log(`  ${kinds.join(" ")}`);
}
console.log(`total cost: ${result.cost}, relaxation level: ${result.level}`);

console.log("\n== Degradations ==");
if (result.degradations.length === 0) console.log("none");
for (const d of result.degradations) {
  console.log(`page ${d.page} [${d.constraint}] ${d.detail}`);
}
for (const w of result.warnings) console.log(`warning: ${w}`);

console.log("\n== Certificate ==");
const cert = buildCertificate(result, doc, metrics, cfg);
for (const page of cert.pages) {
  console.log(
    `page ${page.page}: capacity=${page.capacity} body=${page.bodyUsed} footnote=${page.footnoteHeight} items=${page.itemCount}`,
  );
  for (const check of page.checks) console.log(`  - ${check}`);
}
console.log(
  `exhaustive: claimed=${cert.exhaustive.claimedCost} nodesExplored=${cert.exhaustive.nodesExplored}` +
    ` cheaperFound=${cert.exhaustive.cheaperFound} verified=${cert.exhaustive.verified}` +
    ` truncated=${cert.exhaustive.truncated}`,
);

console.log("\n== Footnote cascade (taller footnote pushes body lines) ==");
const tallDoc = applyFootnoteEdit(doc, "f1", "x".repeat(0) + Array.from({ length: 60 }, (_, i) => `w${i}`).join(" "));
const tallResult = paginate(tallDoc, metrics, cfg);
const countLines = (r: typeof result, pageIdx: number) => {
  const page = r.pages[pageIdx];
  let n = 0;
  for (let k = page.start; k < page.end; k++) if (r.items[k].type === "line") n++;
  return n;
};
console.log(`page 0 body lines, short f1: ${countLines(result, 0)}`);
console.log(`page 0 body lines, tall  f1: ${countLines(tallResult, 0)}`);
console.log(`page 0 footnote ids, tall f1: [${tallResult.pages[0].footnoteIds.join(",")}]`);

console.log("\n== Local update + fixpoint ==");
const edited = applyEdit(doc, { nodeId: "p2", replace: para("p2", 90, { seed: 11 }) });
const update = repaginateLocal(doc, result, edited, metrics, cfg);
if (update.ok && update.result) {
  console.log(
    `reused prefix pages: ${update.reusedPages}, fellBackToFull: ${update.fellBackToFull}`,
  );
  console.log(`new page count: ${update.result.pages.length}, cost: ${update.result.cost}`);
  const problems = validateLayout(update.result, edited, metrics, cfg);
  console.log(`validation problems: ${problems.length === 0 ? "none" : problems.join("; ")}`);
} else {
  console.log(`update failed: ${update.reason}`);
}
const fixpoint = resolveFixpoint(edited, metrics, cfg);
console.log(`fixpoint: ${fixpoint.ok ? `converged in ${fixpoint.iterations} iteration(s)` : fixpoint.reason}`);
