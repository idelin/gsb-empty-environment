import { StringColumn } from "../src/string-column.ts";
import { like, startsWith, matchRegex } from "../src/operators.ts";
import { generateUrls } from "./urls.ts";

const ROWS = 100_000;

console.log(`Building column with ${ROWS.toLocaleString()} URL rows...`);
let t0 = performance.now();
const column = StringColumn.from(generateUrls(ROWS));
const buildMs = performance.now() - t0;
console.log(
  `  compact buffer: ${column.bytes.byteLength.toLocaleString()} bytes, ` +
    `offsets: ${column.offsets.byteLength.toLocaleString()} bytes, built in ${buildMs.toFixed(1)} ms`,
);

console.log(`\nFilter: url LIKE 'https://api.%'`);
t0 = performance.now();
const bitmap = like(column, "https://api.%");
const filterMs = performance.now() - t0;
const matches = bitmap.cardinality();
console.log(`  matched ${matches.toLocaleString()} / ${ROWS.toLocaleString()} rows in ${filterMs.toFixed(2)} ms`);
console.log(
  `  bitmap: ${bitmap.byteLength.toLocaleString()} bytes ` +
    `(1 bit/row, ${(ROWS / bitmap.byteLength).toFixed(0)}x smaller than a boolean array)`,
);

console.log(`\nFirst 5 matching rows (decoded only for display):`);
let shown = 0;
bitmap.forEachSet((row) => {
  if (shown++ < 5) console.log(`  [${row}] ${column.get(row)}`);
});

console.log(`\nCross-checks:`);
t0 = performance.now();
const viaStartsWith = startsWith(column, "https://api.");
const startsWithMs = performance.now() - t0;
console.log(`  startsWith('https://api.')      -> ${viaStartsWith.cardinality().toLocaleString()} rows in ${startsWithMs.toFixed(2)} ms`);

t0 = performance.now();
const viaRegex = matchRegex(column, /^https:\/\/api\./);
const regexMs = performance.now() - t0;
console.log(`  matchRegex(/^https:\\/\\/api\\./) -> ${viaRegex.cardinality().toLocaleString()} rows in ${regexMs.toFixed(2)} ms (decode-per-row path)`);
