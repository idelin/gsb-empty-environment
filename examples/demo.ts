import { FoldedTextProjection, FoldIntersectionError } from '../src/folding.ts';
import { positionAt } from '../src/position.ts';

function show(label: string, value: unknown): void {
  console.log(`${label}:`, typeof value === 'string' ? JSON.stringify(value) : value);
}

// --- 1. Nested folds over a small source file (CRLF line endings) ----------
const source = 'class A {\r\n  method() {\r\n    body();\r\n  }\r\n}\r\nafter();';
const classOpen = source.indexOf('{');
const classClose = source.lastIndexOf('}');
const methodOpen = source.indexOf('{', classOpen + 1);
const methodClose = source.indexOf('}', methodOpen);

const projection = new FoldedTextProjection(source, [
  { start: classOpen + 1, end: classClose, placeholder: ' ⋯ ' },
  { start: methodOpen + 1, end: methodClose, placeholder: ' … ' },
]);

console.log('== nested folds (CRLF source) ==');
show('both collapsed  ', projection.getVisibleText());

const [outer, inner] = projection.getFolds();
projection.setCollapsed(outer.id, false);
show('outer expanded  ', projection.getVisibleText());
projection.setCollapsed(inner.id, false);
show('both expanded   ', projection.getVisibleText());
projection.setCollapsed(inner.id, true);

// --- 2. Offset mapping (UTF-16) --------------------------------------------
console.log('\n== offset mapping ==');
const visible = projection.getVisibleText();
const placeholderAt = visible.indexOf(' … ');
show('visible text    ', visible);
console.log('visible offset of placeholder:', placeholderAt);
console.log('maps to original offset      :', projection.toOriginalOffset(placeholderAt));
console.log(
  'selection over placeholder -> original range:',
  projection.mapVisibleRangeToOriginal(placeholderAt, placeholderAt + 3),
);
console.log('position of placeholder in visible text:', positionAt(visible, placeholderAt));

// --- 3. Editing -------------------------------------------------------------
console.log('\n== editing ==');
projection.applyEdit(0, 0, '// header\r\n'); // insert before everything
show('after insert    ', projection.getVisibleText());
console.log('folds after insert:', projection.getFolds());

// --- 4. Intersection handling ------------------------------------------------
console.log('\n== partial intersection ==');
try {
  new FoldedTextProjection('0123456789', [{ start: 2, end: 6 }, { start: 4, end: 8 }]);
} catch (error) {
  console.log('rejected:', (error as FoldIntersectionError).message);
}
const merged = new FoldedTextProjection(
  '0123456789',
  [{ start: 2, end: 6 }, { start: 4, end: 8 }],
  { onIntersection: 'merge' },
);
show('merged projection', merged.getVisibleText());

// --- 5. UTF-16 surrogate safety ----------------------------------------------
console.log('\n== UTF-16 surrogate pairs ==');
const emoji = new FoldedTextProjection('a😀b😀c', [{ start: 1, end: 3 }]);
show('emoji fold      ', emoji.getVisibleText());
try {
  new FoldedTextProjection('a😀b😀c', [{ start: 2, end: 3 }]);
} catch (error) {
  console.log('rejected:', (error as Error).message);
}
