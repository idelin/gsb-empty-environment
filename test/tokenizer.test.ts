import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeBuilder } from '../src/tree-builder.ts';
import { serialize } from '../src/serializer.ts';

function parse(input: string) {
  return new TreeBuilder(input).parse();
}

test('tokenizer decodes named and numeric character references', () => {
  const doc = parse('<p>a &amp; b &#65; &#x42; &#x27;</p>');
  assert.equal(
    serialize(doc),
    '<p>a &amp; b A B \'</p>',
  );
});

test('tokenizer leaves unknown entity references literal', () => {
  const doc = parse('<p>&notanentity; &amp</p>');
  assert.equal(serialize(doc), '<p>&amp;notanentity; &amp;</p>');
});

test('rawtext style consumes inner markup until matching end tag', () => {
  const doc = parse('<style><img src=x></style><p>ok</p>');
  const style = doc.children[0];
  assert.equal(style.type, 'element');
  if (style.type === 'element') {
    assert.equal(style.tagName, 'style');
    const serialized = serialize(style);
    assert.ok(serialized.startsWith('<style>'));
    assert.ok(serialized.endsWith('</style>'));
    assert.ok(serialized.includes('<img'));
    // Markup inside style must never be reachable as a sibling element.
    assert.equal(
      doc.children.filter((node) =>
        node.type === 'element' && node.tagName === 'img').length,
      0,
    );
  }
  assert.equal(
    serialize(doc),
    '<style><img src="x"></style><p>ok</p>',
  );
});

test('script data state machine handles angle brackets in code', () => {
  const doc = parse('<script>if (a < b && c > d) {}</script><p>x</p>');
  const script = doc.children[0];
  assert.equal(script.type, 'element');
  if (script.type === 'element') {
    const serialized = serialize(script);
    assert.ok(serialized.includes('if (a < b && c > d) {}'));
    assert.ok(serialized.startsWith('<script>'));
    assert.ok(serialized.endsWith('</script>'));
  }
});

test('comments terminate correctly and support nested comment markers', () => {
  const doc = parse('<!-- one --><p>a</p><!-- nested <!-- two -->');
  assert.equal(
    serialize(doc),
    '<!-- one --><p>a</p><!-- nested <!- two -->',
  );
});

test('null bytes are replaced with U+FFFD', () => {
  const doc = parse('<p>a\x00b</p>');
  const paragraph = doc.children[0];
  assert.equal(paragraph.type, 'element');
  if (paragraph.type === 'element') {
    const text = paragraph.children[0];
    assert.equal(text.type, 'text');
    if (text.type === 'text') {
      assert.ok(text.data.includes('�'));
    }
  }
});

test('duplicate attributes keep the first occurrence', () => {
  const doc = parse('<p class="a" class="b">x</p>');
  const paragraph = doc.children[0];
  assert.equal(paragraph.type, 'element');
  if (paragraph.type === 'element') {
    assert.equal(paragraph.attributes.length, 1);
    assert.equal(paragraph.attributes[0].value, 'a');
  }
});

test('unquoted and single-quoted attributes parse like browsers', () => {
  const doc = parse("<input value=a\\nb type='text'>");
  const input = doc.children[0];
  assert.equal(input.type, 'element');
  if (input.type === 'element') {
    const values = Object.fromEntries(
      input.attributes.map((attr) => [attr.name, attr.value]),
    );
    assert.equal(values.value, 'a\\nb');
    assert.equal(values.type, 'text');
  }
});
