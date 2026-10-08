import { describe, expect, test } from 'bun:test';
import { Document } from '../src/edit';
import { findPath, reprint, type LeafNode, type BlockNode } from '../src/cst';
import { flatten, referenceModel } from '../src/verify';

const SRC = '# config\nserver:\n  host: old\n  port: 80\n\ncache:\n  ttl: 30\n';

function leaf(doc: Document, path: string[]): LeafNode {
  const node = findPath(doc.parse().root, path);
  if (!node || node.kind !== 'leaf') throw new Error('leaf not found: ' + path);
  return node;
}

function block(doc: Document, path: string[]): BlockNode {
  const node = findPath(doc.parse().root, path);
  if (!node || node.kind !== 'block') throw new Error('block not found: ' + path);
  return node;
}

describe('local leaf edit reprints only the necessary region', () => {
  test('setValue changes exactly the value bytes', () => {
    const doc = new Document(SRC);
    const target = leaf(doc, ['server', 'host']);
    const result = doc.setValue(target, 'new');
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.edit.start).toBe(target.valueStart);
    expect(result.edit.end).toBe(target.valueEnd);
    // every byte outside the value range is untouched
    expect(result.source.slice(0, target.valueStart)).toBe(SRC.slice(0, target.valueStart));
    expect(result.source.slice(target.valueEnd)).toBe(SRC.slice(target.valueEnd));
    expect(result.source).toBe(SRC.replace('host: old', 'host: new'));
  });

  test('reparse after edit: semantics updated, everything else identical', () => {
    const doc = new Document(SRC);
    doc.setValue(leaf(doc, ['server', 'port']), '8080');
    const before = flatten(SRC, new Document(SRC).parse().root);
    const after = flatten(doc.source, doc.parse().root);
    expect(after).toEqual(
      before.map((e) => (e.key === 'port' ? { ...e, value: '8080' } : e)),
    );
    expect(doc.parse().errors).toEqual([]);
  });

  test('empty or multi-line values are rejected without touching the file', () => {
    const doc = new Document(SRC);
    expect(doc.setValue(leaf(doc, ['server', 'host']), '').ok).toBe(false);
    expect(doc.setValue(leaf(doc, ['server', 'host']), 'a\nb').ok).toBe(false);
    expect(doc.source).toBe(SRC);
  });
});

describe('insert and remove whole nested blocks', () => {
  test('insert a nested block: surrounding bytes unchanged, semantics updated', () => {
    const doc = new Document(SRC);
    const server = block(doc, ['server']);
    const result = doc.insertBlock(server, 1, '  auth:\n    user: u1\n');
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    const expected =
      '# config\nserver:\n  host: old\n  auth:\n    user: u1\n  port: 80\n\ncache:\n  ttl: 30\n';
    expect(result.source).toBe(expected);
    // bytes before and after the insertion point are identical
    expect(result.source.slice(0, result.edit.start)).toBe(SRC.slice(0, result.edit.start));
    expect(result.source.slice(result.edit.start + result.edit.text.length)).toBe(
      SRC.slice(result.edit.start),
    );
    // reparse agrees with the reference model
    const { root, errors } = doc.parse();
    expect(errors).toEqual([]);
    expect(flatten(doc.source, root)).toEqual(referenceModel(doc.source).entries);
  });

  test('insert into an empty block and at root level', () => {
    const doc = new Document('empty:\ntop: 1\n');
    const empty = block(doc, ['empty']);
    expect(doc.insertBlock(empty, 0, '  child: 1\n').ok).toBe(true);
    const root = doc.parse().root;
    expect(doc.insertBlock(root, 2, 'last: 2\n').ok).toBe(true);
    expect(doc.source).toBe('empty:\n  child: 1\ntop: 1\nlast: 2\n');
    expect(doc.parse().errors).toEqual([]);
  });

  test('remove a nested block: only its byte range disappears', () => {
    const src = 'a:\n  b:\n    c: 1\n    d: 2\n  e: 3\nf: 4\n';
    const doc = new Document(src);
    const b = block(doc, ['a', 'b']);
    const result = doc.removeNode(b);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.source).toBe('a:\n  e: 3\nf: 4\n');
    expect(result.source).toBe(src.slice(0, b.start) + src.slice(b.end));
    const { root, errors } = doc.parse();
    expect(errors).toEqual([]);
    expect(flatten(doc.source, root)).toEqual(referenceModel(doc.source).entries);
  });

  test('remove then re-insert restores the original bytes', () => {
    const src = 'a:\n  b: 1\n  c: 2\nz: 9\n';
    const doc = new Document(src);
    const c = leaf(doc, ['a', 'c']);
    const removed = doc.removeNode(c);
    expect(removed.ok).toBe(true);
    expect(doc.source).toBe('a:\n  b: 1\nz: 9\n');
    const a = block(doc, ['a']);
    expect(doc.insertBlock(a, 1, '  c: 2\n').ok).toBe(true);
    expect(doc.source).toBe(src);
  });
});

describe('atomic rejection on indentation ambiguity', () => {
  test('inserting a tab-indented block into a space level is rejected', () => {
    // level at visual column 8 established with spaces; a tab hits the same
    // visual column with different characters => ambiguity
    const src = 'server:\n        host: old\n';
    const doc = new Document(src);
    const server = block(doc, ['server']);
    const result = doc.insertBlock(server, 1, '\tevil: 1\n');
    expect(result.ok).toBe(false);
    if (result.ok) return;
    // the document is untouched
    expect(doc.source).toBe(src);
    // diagnostics are located in the original file, at the insertion site
    const diag = result.diagnostics.find((d) => d.code === 'ambiguous-indent');
    expect(diag).toBeDefined();
    expect(diag!.line).toBe(3); // insertion site: start of line 3 (EOF) in the original
    expect(diag!.col).toBe(1);
    expect(src.slice(diag!.start, diag!.end)).toBe(src.slice(diag!.start, diag!.start + 1));
  });

  test('inserting a block whose dedent matches nothing is rejected', () => {
    const doc = new Document(SRC);
    const server = block(doc, ['server']);
    const result = doc.insertBlock(server, 2, '   odd: 1\n');
    expect(result.ok).toBe(false);
    expect(doc.source).toBe(SRC);
    expect(result.ok === false && result.diagnostics[0].code).toBe('unexpected-indent');
  });

  test('a valid insert after a rejected one still works (no state corruption)', () => {
    const doc = new Document(SRC);
    const server = block(doc, ['server']);
    expect(doc.insertBlock(server, 0, '\tbad: 1\n').ok).toBe(false);
    expect(doc.insertBlock(server, 0, '  good: 1\n').ok).toBe(true);
    expect(doc.source).toBe('# config\nserver:\n  good: 1\n  host: old\n  port: 80\n\ncache:\n  ttl: 30\n');
  });
});

describe('parse-modify-reprint-reparse round trip', () => {
  test('every leaf can be rewritten with unchanged bytes elsewhere', () => {
    const src = 'a: 1\nb:\n  c: 2\n  d:\n    e: 3\nf: 4\n';
    for (const path of [['a'], ['b', 'c'], ['b', 'd', 'e'], ['f']]) {
      const doc = new Document(src);
      const target = leaf(doc, path);
      const result = doc.setValue(target, 'X');
      expect(result.ok).toBe(true);
      if (!result.ok) continue;
      // only the value range differs
      expect(result.source.slice(0, target.valueStart)).toBe(src.slice(0, target.valueStart));
      expect(result.source.slice(target.valueEnd)).toBe(src.slice(target.valueEnd));
      // reprint of the reparsed tree is the new source
      const reparsed = doc.parse();
      expect(reparsed.errors).toEqual([]);
      expect(reprint(doc.source, reparsed.root)).toBe(doc.source);
      // semantics: same shape, one value changed
      const before = flatten(src, new Document(src).parse().root);
      const after = flatten(doc.source, reparsed.root);
      expect(after.length).toBe(before.length);
      for (let i = 0; i < before.length; i++) {
        const expected = before[i].key === path[path.length - 1] && before[i].depth === path.length - 1
          ? { ...before[i], value: 'X' }
          : before[i];
        expect(after[i]).toEqual(expected);
      }
    }
  });
});
