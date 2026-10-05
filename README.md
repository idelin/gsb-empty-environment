# jsonq

A dependency-free TypeScript query engine for JSON-compatible trees, with three
execution modes that are verified to agree:

- **reference interpreter** (`evaluate`) — batch evaluation over an in-memory tree;
- **streaming engine** (`streamQuery`) — incremental evaluation over a SAX-style
  event stream (`walkEvents`), with bounded staging and explicit disk spill;
- **incremental engine** (`IncrementalQuery`) — maintains result diffs and
  stable cursors across document edits on a `VersionedDocument`.

Runs on Node.js ≥ 22.6 (type stripping); no runtime dependencies, no `eval`.

## Query language

```
$.store.book[0].title        field / index
$['a/b']['weird key']        quoted keys (slashes, spaces, ~)
$.arr[-1]                    negative index (from the tail)
$.arr[1:4], $.arr[::-1]      slices (Python-style, negative step allowed)
$..author, $..*              recursive descent (same-path hits deduped)
$.book[?(@.price < 10 && !@.isbn)]   predicates: == != < <= > >=, && || !, ()
```

Every result carries its **path** (array of segments; `encodePath` renders an
RFC-6901-style pointer, `formatPath` a JSONPath-style string), so output is
fully traceable. Object keys are unordered (results are order-independent);
arrays are ordered. Syntax errors report the exact position
(`QuerySyntaxError.pos`); document edit errors locate the failing path
(`DocumentEditError.path`).

## Execution guarantees

- **Incremental output**: field/wildcard/non-negative index/slice and
  recursive-descent results are emitted as soon as their node is reached.
- **Bounded staging**: filters stage one candidate element at a time; negative
  indices/slices stage the whole container, because tail positions are only
  known when the container closes. Staging is bounded by `bufferLimit` events
  and then explicitly spilled to JSONL files under `spillDir`.
- **Cancellation**: aborting via `AbortSignal` throws `CancelledError` and
  removes all temporary spill files (completion does too).
- **Budgets**: `BudgetLimits` covers parsing (`parse`), tree expansion
  (`expand`) and predicate comparisons (`compare`), in all three engines.
- **Stable cursors**: a cursor pins `(version, path)`. Old versions are
  retained via structural sharing, so deleting an array element does not
  change what an old cursor resolves to; `prune()` releases old versions.
- **Deep trees**: all traversals are iterative (explicit stacks).

## Usage

```ts
import { parseQuery, evaluate, streamQuery, walkEvents,
         VersionedDocument, IncrementalQuery } from "./src/index.ts";

const query = parseQuery("$..book[?(@.price < 10)].title");
evaluate(doc, query);                                  // batch
for await (const r of streamQuery(walkEvents(doc), query, { bufferLimit: 1024 })) { /* ... */ }

const vdoc = new VersionedDocument(doc);
const inc = new IncrementalQuery(vdoc, query);
inc.sync();                                            // initial results
const cursor = inc.cursor(["store", "book", 0]);
vdoc.applyEdits([{ type: "delete", path: ["store", "book", 0] }]);
inc.sync();                                            // -> { added, removed, changed }
inc.resolve(cursor);                                   // still the old value @ old version
```

## Develop

```
npm test        # node --test test/*.test.ts
npm run example # node examples/demo.ts
```
