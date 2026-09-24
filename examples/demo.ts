import { ShardedText } from "../src/index.ts";

const v1 = ShardedText.from(
  "The quick brown fox\njumps over the lazy dog\r\n😀 emoji line\n",
);

console.log("v1:", JSON.stringify(v1.toString()));
console.log(
  `UTF-16 length=${v1.length}, line breaks=${v1.lineBreakCount}, lines=${v1.lineCount}`,
);

const lineTwoStart = v1.offsetAt({ line: 1, column: 0 });
const v2 = v1.insert(lineTwoStart, "still ");
console.log("v2:", JSON.stringify(v2.toString()));

const dogStart = v2.toString().indexOf("dog");
const v3 = v2.replace(dogStart, dogStart + 3, "cat");
console.log("v3:", JSON.stringify(v3.toString()));
console.log("v1 after later edits:", JSON.stringify(v1.toString()));

const emojiOffset = v1.toString().indexOf("😀");
const position = v1.positionAt(emojiOffset);
console.log(
  `emoji offset ${emojiOffset} -> line ${position.line}, column ${position.column} -> offset ${v1.offsetAt(position)}`,
);

console.log("range chunks of v3 [24, 40):");
for (const chunk of v3.readRange(24, 40)) {
  console.log(`  [${chunk.start}, ${chunk.end}) ${JSON.stringify(chunk.text)}`);
}

try {
  ShardedText.from("a\r\nb").insert(2, "X");
} catch (error) {
  console.log("CRLF middle insert rejected:", (error as Error).message);
}
