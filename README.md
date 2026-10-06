# search-session

Generation-aware paginated search state library (TypeScript, zero dependencies).

## Concepts

The controller tracks two independent coordinates:

- **Generation** — bumped on every `search(query)`. Page responses stamped
  with an older generation are discarded on arrival, so stale queries can
  never corrupt the current result list.
- **Page cursor** — the page number within one generation. Pages may resolve
  in any order; state is always reassembled strictly by page number starting
  at page 0.

Derived guarantees:

- **Completion is gap-safe** — `complete` is true only when the contiguous
  run of loaded pages from page 0 reaches a terminal page
  (`hasMore: false` or an empty page). A missing or failed page in between
  always blocks completion.
- **Retry** — a failed page keeps its slot and error; `retryPage(page)`
  re-fetches it in the same generation.
- **Dedup** — items are deduplicated by `id`, first occurrence keeps its
  position.

## API

```ts
import { SearchController } from "./src/searchController.ts";

const controller = new SearchController(async (request, page) => {
  // request: { query, tokens, generation }
  const res = await fetch(`/api/search?q=${request.tokens.join("+")}&page=${page}`);
  return res.json(); // { items: [{ id, ... }], hasMore: boolean }
});

controller.subscribe((state) => render(state));
controller.search('hello "big world"'); // generation 1, requests page 0
controller.loadNextPage();              // requests the next never-requested page
controller.retryPage(1);                // re-fetch a failed page
```

`state` exposes `generation`, `query`, `tokens`, `items`, `pageStatuses`,
`pageErrors`, `failedPages`, `loading` and `complete`.

Query tokenization (`src/tokenize.ts`): whitespace-separated terms,
`"quoted spans"` kept as a single term.

## Tests

Requires Node >= 23.6 (or >= 22.6 with `--experimental-strip-types`) to run
TypeScript directly:

```sh
npm test
```

Tests drive the controller with a `ScriptedFetcher` (`test/helpers.ts`) that
queues every page request and resolves/rejects them manually, giving full
control over response ordering to verify tokenization, out-of-order pages,
stale-generation discard, retry, gap-blocked completion, empty last pages
and id dedup.
