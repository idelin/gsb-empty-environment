import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ByteReader, PdfDocument } from '../src/parser.ts';
import { buildSignedContract, buildWithDocTimestamp } from './fixtures.ts';
import { buildXrefStreamPdf } from './xref-stream-builder.ts';

test('parses the header and a single revision', () => {
  const baseline = buildSignedContract();
  const doc = new PdfDocument(new ByteReader(baseline.bytes));
  assert.equal(doc.headerValid, true);
  assert.equal(doc.revisions.length, 1);
  const revision = doc.revisions[0];
  assert.equal(revision.xrefOffset, 833);
  assert.equal(revision.revisionEnd, baseline.revisionEnd);
  assert.equal(revision.prevOffset, null);
  const inUse = [...revision.entries.values()].filter((entry) => entry.type !== 0);
  assert.equal(inUse.length, 9);
});

test('every in-use xref offset points at a matching object header', () => {
  const baseline = buildSignedContract();
  const doc = new PdfDocument(new ByteReader(baseline.bytes));
  const data = baseline.bytes;
  const decoder = new TextDecoder('latin1');
  for (const [num, entry] of doc.revisions[0].entries) {
    if (entry.type !== 1) continue;
    const header = decoder.decode(data.subarray(entry.offset, entry.offset + 32));
    assert.match(header, new RegExp(`^${num} ${entry.gen} obj`));
  }
});

test('materializes the full incremental revision chain via trailer /Prev', () => {
  const baseline = buildSignedContract();
  const stamped = buildWithDocTimestamp(baseline);
  const doc = new PdfDocument(new ByteReader(stamped));
  assert.equal(doc.revisions.length, 2);
  assert.equal(doc.revisions[0].prevOffset, null);
  assert.equal(doc.revisions[1].prevOffset, doc.revisions[0].xrefOffset);
  assert.ok(doc.revisions[1].revisionEnd > doc.revisions[0].revisionEnd);
});

test('resolves an indirect object by number with correct generation metadata', () => {
  const baseline = buildSignedContract();
  const doc = new PdfDocument(new ByteReader(baseline.bytes));
  const font = doc.getObject(baseline.objectNumbers.font, 0);
  assert.ok(font);
  assert.equal(font?.kind, 'dict');
  if (font?.kind === 'dict') {
    assert.equal(font.entries.get('Subtype')?.kind, 'name');
  }
  const entry = doc.getEntry(baseline.objectNumbers.content, 0);
  assert.equal(entry?.type, 1);
  assert.equal(entry?.gen, 0);
});

test('parses a content stream and preserves its raw bytes', () => {
  const baseline = buildSignedContract();
  const doc = new PdfDocument(new ByteReader(baseline.bytes));
  const content = doc.getObject(baseline.objectNumbers.content, 0);
  assert.equal(content?.kind, 'stream');
  if (content?.kind === 'stream') {
    const text = new TextDecoder('latin1').decode(content.raw);
    assert.ok(text.includes('Original contract terms'));
  }
});

test('resolves the trailer root reference consistently across revisions', () => {
  const baseline = buildSignedContract();
  const stamped = buildWithDocTimestamp(baseline);
  const doc = new PdfDocument(new ByteReader(stamped));
  for (const revision of doc.revisions) {
    const root = revision.trailer.entries.get('Root');
    assert.equal(root?.kind, 'ref');
    if (root?.kind === 'ref') assert.equal(root.num, baseline.objectNumbers.root);
  }
});

test('rejects garbage that is not a PDF', () => {
  const bytes = new TextEncoder().encode('this is definitely not a pdf document');
  assert.throws(() => new PdfDocument(new ByteReader(bytes)));
});

test('parses a PDF 1.5 cross-reference stream and resolves its objects', () => {
  const doc = new PdfDocument(new ByteReader(buildXrefStreamPdf()));
  assert.equal(doc.revisions.length, 1);
  const root = doc.revisions[0].trailer.entries.get('Root');
  assert.equal(root?.kind, 'ref');
  const catalog = doc.getObject(1, 0);
  assert.equal(catalog?.kind, 'dict');
  assert.equal(doc.getEntry(1, 0)?.type, 1);
  assert.equal(doc.getEntry(0, 0)?.type, 0);
});
