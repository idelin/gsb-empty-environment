import { RichTextDocument } from "../src/richtext.ts";

const show = (label: string, doc: RichTextDocument) => {
  console.log(`\n=== ${label} ===`);
  console.log("text:", JSON.stringify(doc.getText()));
  console.log("serialized:", doc.serialize());
};

// 1. Build a document with all three attribute families.
const doc = new RichTextDocument("Hello 😀 world, visit example.com or @alice!");
doc.setBold(0, 5); // "Hello"
doc.setBold(5, 13); // adjacent + astral char -> merged into [0,13)
doc.setLink(22, 33, "https://example.com"); // "example.com"
doc.setEntity(37, 43, "mention", "u1"); // "@alice"
show("initial document", doc);

// 2. Link conflict: a new link covers its range, old keeps residual segments.
doc.setLink(30, 36, "https://new.example"); // "com or" overlaps old link tail
show("after overlapping setLink (old keeps residual)", doc);

// 3. Indivisible entity: editing inside invalidates the whole entity.
const events = doc.insert(40, "X"); // inside "@alice"
console.log("\n=== insert inside entity ===");
console.log("events:", JSON.stringify(events));
show("entity invalidated as a whole", doc);

// 4. CRLF + surrogate-safe edits.
const doc2 = new RichTextDocument("a😀\r\nb");
doc2.setBold(4, 5); // "b" (😀 = 2 UTF-16 units, \r\n = 2)
show("CRLF + surrogate pair document", doc2);
try {
  doc2.insert(2, "x"); // between the surrogate halves
} catch (err) {
  console.log("\nsplitting a surrogate pair is rejected:");
  console.log(" ", (err as Error).message);
}

// 5. Canonical serialization is insertion-order independent.
const a = new RichTextDocument("hello world");
a.setBold(0, 5);
a.setBold(5, 11);
a.setLink(0, 5, "https://a");
const b = new RichTextDocument("hello world");
b.setLink(0, 5, "https://a");
b.setBold(3, 11);
b.setBold(0, 3);
console.log("\n=== canonical serialization ===");
console.log("doc A:", a.serialize());
console.log("doc B:", b.serialize());
console.log("identical:", a.serialize() === b.serialize());
