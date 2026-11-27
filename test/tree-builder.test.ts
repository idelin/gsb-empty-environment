import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeBuilder } from '../src/tree-builder.ts';
import { serialize } from '../src/serializer.ts';
import { verifyIdempotency } from '../src/idempotency.ts';
import { NS_HTML, NS_MATHML, NS_SVG } from '../src/types.ts';
import type { ElementNode } from '../src/types.ts';

function parse(input: string) {
  return new TreeBuilder(input).parse();
}

function elements(input: string): ElementNode[] {
  const out: ElementNode[] = [];
  const visit = (nodes: ElementNode['children'] | ElementNode[]): void => {
    for (const node of nodes) {
      if (node.type === 'element') {
        out.push(node);
        visit(node.children);
      }
    }
  };
  visit(parse(input).children);
  return out;
}

test('math and svg roots receive the correct namespaces', () => {
  const found = elements(
    '<math><mrow><mi>x</mi></mrow></math><svg><circle r="2"/></svg>',
  );
  const math = found.find((node) => node.tagName === 'math');
  const mrow = found.find((node) => node.tagName === 'mrow');
  const svg = found.find((node) => node.tagName === 'svg');
  const circle = found.find((node) => node.tagName === 'circle');
  assert.equal(math?.namespace, NS_MATHML);
  assert.equal(mrow?.namespace, NS_MATHML);
  assert.equal(svg?.namespace, NS_SVG);
  assert.equal(circle?.namespace, NS_SVG);
});

test('mtext is a MathML text integration point for breakout tags', () => {
  const found = elements(
    '<math><mtext><b>bold</b> normal</mtext></math>',
  );
  const bold = found.find((node) => node.tagName === 'b');
  assert.equal(bold?.namespace, NS_HTML);
});

test('mi/mo/mn/ms behave as MathML text integration points', () => {
  for (const tag of ['mi', 'mo', 'mn', 'ms']) {
    const found = elements(
      `<math><${tag}><span>x</span></${tag}></math>`,
    );
    const span = found.find((node) => node.tagName === 'span');
    assert.equal(span?.namespace, NS_HTML, tag);
  }
});

test('svg desc/title are HTML integration points', () => {
  const found = elements(
    '<svg><desc><img src="x"></desc><title><b>t</b></title></svg>',
  );
  const image = found.find((node) => node.tagName === 'img');
  const bold = found.find((node) => node.tagName === 'b');
  assert.equal(image?.namespace, NS_HTML);
  assert.equal(bold?.namespace, NS_HTML);
});

test('svg foreignObject is an HTML integration point', () => {
  const found = elements(
    '<svg><foreignObject><div>html</div></foreignObject></svg>',
  );
  const div = found.find((node) => node.tagName === 'div');
  assert.equal(div?.namespace, NS_HTML);
});

test('annotation-xml with text/html encoding is an integration point', () => {
  const found = elements(
    '<math><annotation-xml encoding="text/html">' +
      '<span>x</span></annotation-xml></math>',
  );
  const span = found.find((node) => node.tagName === 'span');
  assert.equal(span?.namespace, NS_HTML);
});

test('table breakout inside MathML foster-parents later foreign nodes', () => {
  const input =
    '<math><mtext><table><mglyph><style><!--</style>' +
    '<img src=x onerror=alert(1)>';
  const builder = new TreeBuilder(input);
  const doc = builder.parse();
  const topLevel = doc.children.map((node) =>
    node.type === 'element' ? node.tagName : node.type,
  );
  assert.deepEqual(topLevel[0], 'math');
  assert.deepEqual(topLevel[1], 'mglyph');
  assert.deepEqual(topLevel[2], 'table');
  const breakouts = builder.structuralEvents.filter(
    (event) => event.type === 'namespace-breakout',
  );
  assert.ok(breakouts.some((event) => event.tagName === 'table'));
  const fosters = builder.structuralEvents.filter(
    (event) => event.type === 'foster-parent',
  );
  assert.ok(fosters.some((event) => event.tagName === 'mglyph'));
  assert.ok(verifyIdempotency(doc, serialize(doc)).stable);
});

test('ordinary tables materialize an implicit tbody and stay idempotent', () => {
  const doc = parse('<table><tr><td>a</td></tr></table>');
  const serialized = serialize(doc);
  assert.equal(
    serialized,
    '<table><tbody><tr><td>a</td></tr></tbody></table>',
  );
  assert.ok(verifyIdempotency(doc, serialized).stable);
});

test('SVG tag and attribute names are adjusted per spec', () => {
  const found = elements(
    '<svg><clippath><lineargradient gradientunits="x">' +
      '</lineargradient></clippath></svg>',
  );
  const clipPath = found.find((node) => node.tagName === 'clipPath');
  const gradient = found.find(
    (node) => node.tagName === 'linearGradient',
  );
  assert.ok(clipPath);
  assert.ok(gradient);
  assert.ok(
    gradient?.attributes.some(
      (attribute) => attribute.name === 'gradientUnits',
    ),
  );
});

test('foreign self-closing leaves serialize explicitly self-closed', () => {
  const doc = parse('<svg><circle r="1"/></svg>');
  const serialized = serialize(doc);
  assert.equal(serialized, '<svg><circle r="1"/></svg>');
  assert.ok(verifyIdempotency(doc, serialized).stable);
});
