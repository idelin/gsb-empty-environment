// Demo: build a segment, validate it, run queries, and print how many
// posting blocks were actually decoded (jump-table effectiveness).
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { SegmentWriter } from "./src/writer.ts";
import { SegmentReader } from "./src/reader.ts";

const w = new SegmentWriter();
for (let i = 0; i < 1000; i++) {
  if (i % 2 === 0) w.add("even", i);
  if (i % 3 === 0) w.add("three", i);
  if (i % 7 === 0) w.add("seven", i);
}
const path = join(mkdtempSync(join(tmpdir(), "cidx-demo-")), "seg.cidx");
w.write(path, { blockSize: 16 });

const reader = SegmentReader.open(path);
console.log(`segment: ${reader.termCount} terms`);

reader.stats.decodedBlocks = 0;
const hits = reader.andQuery(["even", "three", "seven"]);
console.log(`AND(even, three, seven) -> [${hits.join(", ")}]`);
console.log(`decoded blocks for AND query: ${reader.stats.decodedBlocks}`);

reader.stats.decodedBlocks = 0;
const c = reader.cursor("even")!;
c.seek(990);
console.log(`seek(even, 990) -> ${c.value}, decoded blocks: ${reader.stats.decodedBlocks} (of ${Math.ceil(500 / 16)} in the list)`);
