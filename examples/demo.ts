import {
  IncrementalQuery,
  VersionedDocument,
  collectStream,
  evaluate,
  formatPath,
  parseQuery,
  walkEvents,
  type JsonValue,
} from "../src/index.ts";

const doc: JsonValue = {
  store: {
    book: [
      { title: "Sayings of the Century", author: "Nigel Rees", price: 8.95 },
      { title: "Sword of Honour", author: "Evelyn Waugh", price: 12.99 },
      { title: "Moby Dick", author: "Herman Melville", price: 8.99, isbn: "0-553-21311-3" },
    ],
    "weird/key": { note: "keys may contain slashes" },
  },
};

function show(title: string, rows: Array<{ path: (string | number)[]; value: JsonValue }>): void {
  console.log(`\n== ${title} ==`);
  for (const r of rows) console.log(`${formatPath(r.path)}  =>  ${JSON.stringify(r.value)}`);
  if (rows.length === 0) console.log("(no results)");
}

// 1. Reference interpreter.
show("interpreter: $..book[?(@.price < 10)].title", evaluate(doc, parseQuery("$..book[?(@.price < 10)].title")));

// 2. Streaming over an event stream gives identical results, incrementally.
const streamed = await collectStream(walkEvents(doc), parseQuery("$..author"));
show("streaming: $..author", streamed);

// 3. Keys with slashes, negative indices, slices.
show("slash key: $.store['weird/key'].note", evaluate(doc, parseQuery("$.store['weird/key'].note")));
show("negative index: $.store.book[-1].title", evaluate(doc, parseQuery("$.store.book[-1].title")));
show("slice: $.store.book[0:3:2].title", evaluate(doc, parseQuery("$.store.book[0:3:2].title")));

// 4. Incremental diffs + stable cursors over document edits.
const vdoc = new VersionedDocument(doc);
const inc = new IncrementalQuery(vdoc, "$..book[*].title");
inc.sync();
const cursor = inc.cursor(["store", "book", 0]);

vdoc.applyEdits([
  { type: "set", path: ["store", "book", 1, "title"], value: "Sword of Honour (revised)" },
  { type: "delete", path: ["store", "book", 0] },
]);
const diff = inc.sync();
console.log("\n== incremental diff after edit ==");
for (const r of diff.added) console.log(`+ ${formatPath(r.path)} => ${JSON.stringify(r.value)}`);
for (const r of diff.removed) console.log(`- ${formatPath(r.path)} => ${JSON.stringify(r.value)}`);
for (const c of diff.changed) {
  console.log(`~ ${formatPath(c.path)} => ${JSON.stringify(c.oldValue)} -> ${JSON.stringify(c.newValue)}`);
}

// 5. The old cursor still resolves against its own version.
const resolved = inc.resolve(cursor);
console.log(`\ncursor ${formatPath(cursor.path)} @v${cursor.version} still resolves to: ${resolved.ok ? JSON.stringify(resolved.value) : "<gone>"}`);
