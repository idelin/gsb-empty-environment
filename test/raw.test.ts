import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  col,
  eq,
  field,
  ident,
  param,
  raw,
  render,
  RenderError,
  select,
  verifyRoundTrip,
} from '../src/index.ts';

test('raw fragments render verbatim and bind nothing', () => {
  const q = select({
    columns: [col(raw('COUNT(*)'), 'n')],
    from: [ident('t')],
    where: eq(field('t.a'), param(1)),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT COUNT(*) AS "n" FROM "t" WHERE ("t"."a" = $1)');
  assert.deepEqual(r.bindings, [1]);
  assert.ok(verifyRoundTrip(q).ok);
});

test('raw text cannot inject placeholders ($n)', () => {
  const q = select({
    columns: [raw('x = $1')],
    from: [ident('t')],
  });
  assert.throws(() => render(q), RenderError);
});

test('raw text cannot inject placeholders (?)', () => {
  const q = select({
    columns: [raw('x = ?')],
    from: [ident('t')],
  });
  assert.throws(() => render(q), RenderError);
});

test('raw fragments cannot smuggle values into the SQL', () => {
  // The Raw API accepts only a static string; there is no interpolation
  // entry point. A value-looking string stays inert text, and bindings
  // remain unaffected.
  const q = select({
    columns: [raw("0"), col(raw('1'), 'one')],
    from: [ident('t')],
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT 0, 1 AS "one" FROM "t"');
  assert.deepEqual(r.bindings, []);
});
