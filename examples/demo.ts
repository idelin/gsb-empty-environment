/**
 * Runnable demo: node examples/demo.ts  (Node >= 23, no build step)
 */
import { TreeModel } from "../src/model.ts";
import { TreeView } from "../src/view.ts";
import { layoutRows } from "../src/metrics.ts";
import { DeferredLoader, flush, lazyChildrenFixture, metricsFixture, treeFixture } from "../src/fixtures.ts";

const loader = new DeferredLoader(lazyChildrenFixture());
const model = new TreeModel(treeFixture(), loader.load);
const view = new TreeView(model);
const metrics = metricsFixture();

function show(title: string): void {
  console.log(`\n== ${title} ==`);
  const focus = view.focus();
  const selected = new Set(view.selection());
  for (const row of layoutRows(model, metrics, 0, model.totalVisible)) {
    const node = model.get(row.id)!;
    const marks = `${row.id === focus ? ">" : " "}${selected.has(row.id) ? "*" : " "}${node.disabled ? " (disabled)" : ""}${node.loading ? " (loading)" : ""}`;
    const icon = node.children.length > 0 || node.lazy ? (node.expanded ? "▾" : "▸") : "·";
    console.log(`${" ".repeat(row.depth * 2)}${icon} ${node.label}${marks}`);
  }
}

view.toggle("src");
view.toggle("docs");
view.toggle("scripts");
show("expand src, docs, scripts");

view.home();
view.moveDown();
view.moveDown();
view.moveDown(); // skips disabled util.ts
show("home + 3x down (disabled rows skipped)");

view.select("app");
view.selectRange("build"); // shift-range in visible order, skips disabled rows
show("select app, then shift-click build.sh");

console.log("\n-- expanding lib triggers an async load; user collapses it before it resolves --");
view.toggle("lib");
view.toggle("lib"); // collapse while loading
loader.resolveNext("lib");
await flush();
show("stale load applied: branch stays collapsed, selection preserved");

view.toggle("lib"); // children appear instantly
show("re-expand lib (children already loaded)");

view.type("a");
view.type("a"); // repeat -> cycle to next match
show(`type-ahead "aa" cycling (focus on '${view.focus()}')`);

model.setFilter((label) => label.includes("md"));
show('filter: label includes "md" (ancestors kept)');
model.setFilter(null);

view.remove(view.focus()!); // focus is on api.md
show("delete the focused node -> deterministic fallback");

console.log(`\nversion=${model.version} fullRecomputes=${model.stats.fullRecomputes} totalVisible=${model.totalVisible}`);
