## annotation-ranges

Edit-tracking annotation (comment) ranges with per-endpoint affinity, UTF-16
offsets, batched edits and undo. Pure TypeScript, zero dependencies; runs
directly on Node.js >= 23.6 via native type stripping.

### Model

- Each annotation is a half-open range `[start, end)` over UTF-16 code-unit
  offsets (JavaScript string indices). Offsets that would split a surrogate
  pair are rejected.
- Each endpoint has an affinity:
  - `inward` — sticks to the annotated content: text inserted exactly at the
    endpoint is **not** included in the range.
  - `outward` — sticks to the surrounding text: text inserted exactly at the
    endpoint **is** included.
- Deleting everything a range covers collapses it to a zero-width anchor at
  the deletion point; the anchor keeps tracking later edits and the range
  length can never go negative.
- `applyEdits(edits)` takes a batch whose offsets refer to the pre-batch
  document. Edits apply ordered by `(offset, id)`, so same-position inserts
  concatenate in ascending operation-ID order. A batch is transactional and
  a single `undo()` step; undo restores the text and the original anchors.
- `offsetAtPosition` / `positionAtOffset` convert between `(line, character)`
  and UTF-16 offsets for both LF and CRLF line endings.

### Usage

```ts
import { AnnotationStore } from './src/index.ts';

const store = new AnnotationStore('abcdef');
store.addAnnotation({ id: 'r', start: 2, end: 4, startAffinity: 'outward', endAffinity: 'inward' });
store.applyEdits([{ id: 1, offset: 2, deleteCount: 0, insertText: 'XY' }]);
store.getAnnotation('r'); // { start: 2, end: 6, ... } — inserted text included
store.undo();             // back to [2, 4)
```

### Commands

```sh
npm test        # node --test test/*.test.ts
npm run sample  # node examples/sample.ts
```

### Sample run (`npm run sample`)

```text
1a. insert "XY" at left end, startAffinity=inward (excluded)
  text="abXYcdef"  range=[4, 6)  covered="cd"
1b. insert "XY" at left end, startAffinity=outward (included)
  text="abXYcdef"  range=[2, 6)  covered="XYcd"
2. delete [1,5) swallows the range -> zero-width anchor
  text="af"  range=[1, 1)  covered=""
3. same-offset inserts ordered by op id -> "a123c"
4a. after batch (replace greeting, append "!")
  text="Hi world!"  range=[3, 9)  covered="world!"
4b. after undo, anchors restored
  text="hello world"  range=[6, 11)  covered="world"
5. CRLF doc, emoji line inserted (emoji = 2 UTF-16 units)
  text="😀\r\none\r\ntwo\r\nthree"  range=[9, 12)  covered="two"
   anchor position: {"line":2,"character":0}
```

### Test results (`npm test`, Node v23.9.0)

```text
ℹ tests 21
ℹ suites 7
ℹ pass 21
ℹ fail 0
```
