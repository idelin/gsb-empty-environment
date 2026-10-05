import { readFileSync } from "node:fs";
import { diffTokens } from "./diff.ts";

function tokenize(text: string, granularity: string): string[] {
  if (granularity === "words") return text.split(/\s+/).filter((t) => t.length > 0);
  return text.split(/\r?\n/);
}

const [, , oldPath, newPath, granularity = "lines"] = process.argv;
if (!oldPath || !newPath) {
  console.error("usage: node --experimental-strip-types src/cli.ts <oldFile> <newFile> [lines|words]");
  process.exit(2);
}

const oldTokens = tokenize(readFileSync(oldPath, "utf8"), granularity);
const newTokens = tokenize(readFileSync(newPath, "utf8"), granularity);
const result = diffTokens(oldTokens, newTokens);

interface NewSideItem {
  at: number;
  kind: string;
  detail: string;
}
const items: NewSideItem[] = [];
for (const b of result.blocks) {
  items.push({
    at: b.newStart,
    kind: b.kind === "move" ? "MOVE" : "KEEP",
    detail: `old[${b.oldStart}..${b.oldStart + b.length}) -> new[${b.newStart}..${b.newStart + b.length})  ${b.tokens.join(" / ")}`,
  });
}
for (const r of result.insertions) {
  items.push({
    at: r.start,
    kind: "INS",
    detail: `new[${r.start}..${r.start + r.length})  ${r.tokens.join(" / ")}`,
  });
}
items.sort((a, b) => a.at - b.at);
for (const item of items) console.log(`${item.kind.padEnd(5)} ${item.detail}`);
for (const r of result.deletions) {
  console.log(`DEL   old[${r.start}..${r.start + r.length})  ${r.tokens.join(" / ")}`);
}
console.log(
  `# preserved=${result.coveredTokens} blocks=${result.blocks.length} optimal=${result.optimal} states=${result.statesExplored}`,
);
