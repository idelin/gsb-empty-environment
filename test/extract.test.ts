import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { SnapshotAttribute, SnapshotNode } from '../src/types.ts';
import { extractPrices } from '../src/extract.ts';

function el(
  id: string,
  tagName: string,
  attributes: Record<string, SnapshotAttribute> = {},
  children: SnapshotNode[] = [],
  extra: Partial<SnapshotNode> = {},
): SnapshotNode {
  return { id, tagName, attributes, children, ...extra };
}

function textNode(id: string, text: string, extra: Partial<SnapshotNode> = {}): SnapshotNode {
  return { id, type: 'text', text, ...extra };
}

function page(children: SnapshotNode[]): SnapshotNode {
  return el('page', 'div', {}, children);
}

const usdRules = {
  productScope: '[data-product]',
  currentPriceMarker: '.current-price',
  originalPriceMarker: '.old-price',
  currency: 'USD',
  numberFormat: { decimal: '.', grouping: ',' },
} as const;

test('resolves current price and rejects struck original price with exact traces', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1', class: 'product-card' }, [
    el('current-wrap', 'span', { class: 'current-price' }, [
      textNode('current-text', '$1,234.50'),
    ]),
    el('old-wrap', 'span', { class: 'old-price', style: { 'text-decoration-line': 'line-through' } }, [
      textNode('old-text', '$1,499.00'),
    ]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 1);
  const price = result.prices[0];
  assert.equal(price.value, 1234.5);
  assert.equal(price.currency, 'USD');
  assert.equal(price.nodeId, 'current-text');
  assert.equal(price.start, 1);
  assert.equal(price.end, 9);
  assert.equal(price.span.text, '1,234.50');
  assert.equal(price.currencyTrace?.nodeId, 'current-text');
  assert.equal(price.currencyTrace?.start, 0);
  assert.equal(price.currencyTrace?.end, 1);

  const oldCandidate = result.candidates.find((candidate) => candidate.nodeId === 'old-text');
  assert.ok(oldCandidate);
  assert.equal(oldCandidate.eligible, false);
  assert.ok(oldCandidate.reasons.includes('strikethrough'));
  assert.ok(oldCandidate.reasons.includes('original-price'));
});

test('nested product scopes never combine or borrow fields', () => {
  const inner = el('inner-card', 'div', { 'data-product': 'inner' }, [
    el('inner-price', 'span', { class: 'current-price' }, [textNode('inner-text', '$20.00')]),
  ]);
  const outer = el('outer-card', 'div', { 'data-product': 'outer' }, [
    el('outer-price', 'span', { class: 'current-price' }, [textNode('outer-text', '$10.00')]),
    inner,
  ]);
  const result = extractPrices({ root: page([outer]) }, usdRules);

  assert.equal(result.prices.length, 2);
  const outerProduct = result.products.find((product) => product.scopeId === 'outer-card');
  const innerProduct = result.products.find((product) => product.scopeId === 'inner-card');
  assert.equal(outerProduct?.price?.value, 10);
  assert.equal(innerProduct?.price?.value, 20);
  assert.equal(outerProduct?.candidates.some((candidate) => candidate.nodeId === 'inner-text'), false);
});

test('outer card without its own price does not absorb a nested card price', () => {
  const inner = el('inner-card', 'div', { 'data-product': 'inner' }, [
    el('inner-price', 'span', { class: 'current-price' }, [textNode('inner-text', '$20.00')]),
  ]);
  const outer = el('outer-card', 'div', { 'data-product': 'outer' }, [inner]);
  const result = extractPrices({ root: page([outer]) }, usdRules);

  const outerProduct = result.products.find((product) => product.scopeId === 'outer-card');
  const innerProduct = result.products.find((product) => product.scopeId === 'inner-card');
  assert.equal(outerProduct?.status, 'missing');
  assert.equal(outerProduct?.candidates.length, 0);
  assert.equal(innerProduct?.price?.value, 20);
});

test('hidden ancestors are visible evidence but never a current price', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('hidden-wrap', 'div', { style: { display: 'none' } }, [
      el('hidden-price', 'span', { class: 'current-price' }, [textNode('hidden-text', '$10.00')]),
    ]),
    el('visible-price', 'span', { class: 'current-price' }, [textNode('visible-text', '$20.00')]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].value, 20);
  const hidden = result.candidates.find((candidate) => candidate.nodeId === 'hidden-text');
  assert.ok(hidden);
  assert.equal(hidden.eligible, false);
  assert.ok(hidden.reasons.includes('hidden'));
  assert.ok(hidden.reasons.includes('hidden-ancestor'));
  assert.ok(hidden.reasons.includes('display-none'));
});

test('template contents and placeholder values cannot supply a current price', () => {
  const template = el('price-template', 'template', { 'data-template-id': 'price-template' }, [
    el('template-price', 'span', { class: 'current-price' }, [textNode('template-text', '$0.00')]),
  ]);
  const placeholder = el('placeholder-price', 'span', { class: 'current-price', 'data-placeholder': 'true' }, [
    textNode('placeholder-text', '{{price}}'),
  ]);
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    template,
    placeholder,
    el('real-price', 'span', { class: 'current-price' }, [textNode('real-text', '$25.00')]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].value, 25);
  const templateCandidate = result.candidates.find((candidate) => candidate.nodeId === 'template-text');
  assert.ok(templateCandidate);
  assert.equal(templateCandidate.eligible, false);
  assert.ok(templateCandidate.reasons.includes('template'));
  const placeholderCandidate = result.candidates.find((candidate) => candidate.nodeId === 'placeholder-text');
  assert.ok(placeholderCandidate);
  assert.equal(placeholderCandidate.eligible, false);
  assert.ok(placeholderCandidate.reasons.includes('placeholder'));
  assert.equal(placeholderCandidate.value, undefined);
});

test('a current marker on struck text is still rejected', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('struck-current', 's', { class: 'current-price' }, [textNode('struck-text', '$30.00')]),
    el('real-price', 'span', { class: 'current-price' }, [textNode('real-text', '$25.00')]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].value, 25);
  const struck = result.candidates.find((candidate) => candidate.nodeId === 'struck-text');
  assert.ok(struck);
  assert.equal(struck.eligible, false);
  assert.ok(struck.reasons.includes('strikethrough'));
});

test('a style-only line-through is still rejected', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('struck', 'span', { class: 'current-price' }, [textNode('struck-text', '$30.00')], {
      style: { 'text-decoration-line': 'line-through' },
    }),
    el('real', 'span', { class: 'current-price' }, [textNode('real-text', '$25.00')]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].value, 25);
  const struck = result.candidates.find((candidate) => candidate.nodeId === 'struck-text');
  assert.ok(struck);
  assert.ok(struck.reasons.includes('strikethrough'));
});

test('repeated amounts across cards remain isolated and repeated identical values agree', () => {
  const cardA = el('card-a', 'div', { 'data-product': 'a' }, [
    el('price-a', 'span', { class: 'current-price' }, [textNode('text-a', '$19.99')]),
    el('old-a', 'span', { class: 'old-price', style: { 'text-decoration': 'line-through' } }, [textNode('old-text-a', '$19.99')]),
    el('price-a-duplicate', 'span', { class: 'current-price' }, [textNode('text-a-duplicate', '$19.99')]),
  ]);
  const cardB = el('card-b', 'div', { 'data-product': 'b' }, [
    el('price-b', 'span', { class: 'current-price' }, [textNode('text-b', '$19.99')]),
  ]);
  const result = extractPrices({ root: page([cardA, cardB]) }, usdRules);

  assert.equal(result.prices.length, 2);
  assert.deepEqual(result.prices.map((price) => price.scopeId).sort(), ['card-a', 'card-b']);
  assert.equal(result.prices.every((price) => price.value === 19.99), true);
  const cardAProduct = result.products.find((product) => product.scopeId === 'card-a');
  assert.equal(cardAProduct?.price?.evidence.length, 2);
  assert.equal(cardAProduct?.candidates.some((candidate) => candidate.nodeId === 'old-text-a' && candidate.eligible), false);
});

test('multiple distinct marked amounts produce candidates, not a guessed price', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price', 'span', { class: 'current-price' }, [textNode('price-text', '$10.00 / $20.00')]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 0);
  assert.equal(result.products[0].status, 'ambiguous');
  assert.equal(result.ambiguous.length, 2);
  assert.deepEqual(result.ambiguous.map((candidate) => candidate.value), [10, 20]);
});

test('mixed-currency page does not relabel a foreign amount', () => {
  const usdCard = el('usd-card', 'div', { 'data-product': 'usd' }, [
    el('usd-price', 'span', { class: 'current-price' }, [textNode('usd-text', '$19.99')]),
  ]);
  const eurCard = el('eur-card', 'div', { 'data-product': 'eur' }, [
    el('eur-price', 'span', { class: 'current-price' }, [textNode('eur-text', '€19.99')]),
  ]);
  const result = extractPrices({ root: page([usdCard, eurCard]) }, usdRules);

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].currency, 'USD');
  const eurProduct = result.products.find((product) => product.scopeId === 'eur-card');
  assert.equal(eurProduct?.status, 'invalid');
  const eurCandidate = result.candidates.find((candidate) => candidate.nodeId === 'eur-text');
  assert.ok(eurCandidate);
  assert.equal(eurCandidate.eligible, false);
  assert.ok(eurCandidate.reasons.includes('currency-mismatch'));
});

test('mixed currencies inside one product scope stay ambiguous', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('usd-price', 'span', { class: 'current-price' }, [textNode('usd-text', '$10.00')]),
    el('eur-price', 'span', { class: 'current-price' }, [textNode('eur-text', '€20.00')]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 0);
  assert.equal(result.products[0].status, 'ambiguous');
  assert.equal(result.ambiguous.length, 2);
});

test('currency may be combined from a sibling field in the same product scope', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price-wrap', 'div', { class: 'current-price' }, [
      el('currency', 'span', {}, [textNode('currency-text', '$')]),
      el('amount', 'span', {}, [textNode('amount-text', '19.99')]),
    ]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].value, 19.99);
  assert.equal(result.prices[0].currency, 'USD');
  assert.equal(result.prices[0].currencyTrace?.nodeId, 'currency-text');
  assert.equal(result.prices[0].nodeId, 'amount-text');
});

test('currency never crosses into another product scope', () => {
  const cardA = el('card-a', 'div', { 'data-product': 'a' }, [
    el('price-a', 'span', { class: 'current-price' }, [textNode('text-a', '19.99')]),
  ]);
  const cardB = el('card-b', 'div', { 'data-product': 'b' }, [
    el('currency-b', 'span', {}, [textNode('currency-text-b', 'USD')]),
    el('price-b', 'span', { class: 'current-price' }, [textNode('text-b', '19.99')]),
  ]);
  const result = extractPrices({ root: page([cardA, cardB]) }, {
    productScope: '[data-product]',
    currentPriceMarker: '.current-price',
    numberFormat: { decimal: '.', grouping: ',' },
  });

  const productA = result.products.find((product) => product.scopeId === 'card-a');
  const productB = result.products.find((product) => product.scopeId === 'card-b');
  assert.equal(productA?.status, 'invalid');
  const candidateA = productA?.candidates.find((candidate) => candidate.nodeId === 'text-a');
  assert.ok(candidateA);
  assert.ok(candidateA.reasons.includes('missing-currency'));
  assert.equal(productB?.price?.value, 19.99);
  assert.equal(productB?.price?.currency, 'USD');
});

test('configured comma-decimal and dot-grouping parse without guessing', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price-wrap', 'span', { class: 'current-price' }, [textNode('price-text', '€1.234,56')]),
  ]);
  const result = extractPrices({ root: page([card]) }, {
    productScope: '[data-product]',
    currentPriceMarker: '.current-price',
    currency: 'EUR',
    numberFormat: { decimal: ',', grouping: '.' },
  });

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].value, 1234.56);
  assert.equal(result.prices[0].currency, 'EUR');
  assert.equal(result.prices[0].span.text, '1.234,56');
  assert.equal(result.prices[0].start, 1);
  assert.equal(result.prices[0].end, 9);
});

test('unconfigured grouping is ambiguous and returns an invalid candidate', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price-wrap', 'span', { class: 'current-price' }, [textNode('price-text', '$1,234')]),
  ]);
  const result = extractPrices({ root: page([card]) }, {
    productScope: '[data-product]',
    currentPriceMarker: '.current-price',
    currency: 'USD',
    numberFormat: { decimal: '.', grouping: null },
  });

  assert.equal(result.prices.length, 0);
  const candidate = result.candidates.find((entry) => entry.nodeId === 'price-text');
  assert.ok(candidate);
  assert.equal(candidate.eligible, false);
  assert.ok(candidate.reasons.includes('invalid-number'));
  assert.equal(candidate.issues[0]?.code, 'unconfigured-separator');
});

test('attributes retain their own provenance and trace interval', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price-meta', 'meta', {
      itemprop: 'price',
      content: { value: '1,234.50', source: 'dom' },
      'data-currency': 'USD',
    }),
  ]);
  const result = extractPrices({ root: page([card]) }, {
    ...usdRules,
    currentPriceMarker: '[itemprop="price"]',
  });

  assert.equal(result.prices.length, 1);
  const price = result.prices[0];
  assert.equal(price.value, 1234.5);
  assert.equal(price.currency, 'USD');
  assert.equal(price.nodeId, 'price-meta');
  assert.equal(price.span.source, 'attribute');
  assert.equal(price.span.attribute, 'content');
  assert.equal(price.start, 0);
  assert.equal(price.end, 8);
});

test('template-provenance attributes cannot be the current price', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('template-attr', 'meta', {
      class: 'current-price',
      'data-price': { value: '19.99', source: 'template' },
    }),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 0);
  const candidate = result.candidates.find((entry) => entry.nodeId === 'template-attr');
  assert.ok(candidate);
  assert.equal(candidate.eligible, false);
  assert.ok(candidate.reasons.includes('attribute-provenance'));
  assert.equal(result.products[0].status, 'invalid');
});

test('currentPriceAttribute can act as the current marker and amount source', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price', 'span', { 'data-current-price': '19.99' }),
  ]);
  const result = extractPrices({ root: page([card]) }, {
    productScope: '[data-product]',
    currentPriceAttribute: 'data-current-price',
    currency: 'USD',
    numberFormat: { decimal: '.', grouping: ',' },
  });

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].value, 19.99);
  assert.equal(result.prices[0].span.source, 'attribute');
  assert.equal(result.prices[0].span.attribute, 'data-current-price');
});

test('unresolved currency evidence yields an ambiguous product', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price', 'span', { class: 'current-price' }, [textNode('price-text', '$10.00')]),
  ]);
  const result = extractPrices({ root: page([card]) }, {
    productScope: '[data-product]',
    currentPriceMarker: '.current-price',
    numberFormat: { decimal: '.', grouping: ',' },
  });

  assert.equal(result.prices.length, 0);
  assert.equal(result.products[0].status, 'ambiguous');
  assert.equal(result.ambiguous.length, 1);
  assert.ok(result.ambiguous[0].reasons.includes('currency-ambiguous'));
});

test('character intervals are exact even with a prefix label', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('price', 'span', { class: 'current-price' }, [textNode('price-text', 'Sale $1,234.50')]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 1);
  assert.equal(result.prices[0].start, 6);
  assert.equal(result.prices[0].end, 14);
  assert.equal(result.prices[0].span.text, '1,234.50');
  assert.equal(result.prices[0].currencyTrace?.start, 5);
  assert.equal(result.prices[0].currencyTrace?.end, 6);
});

test('hidden visibility flags on an ancestor are propagated', () => {
  const card = el('product-1', 'div', { 'data-product': 'sku-1' }, [
    el('hidden-parent', 'div', { visible: false }, [
      el('price', 'span', { class: 'current-price' }, [textNode('price-text', '$9.99')]),
    ]),
  ]);
  const result = extractPrices({ root: page([card]) }, usdRules);

  assert.equal(result.prices.length, 0);
  const candidate = result.candidates.find((entry) => entry.nodeId === 'price-text');
  assert.ok(candidate);
  assert.ok(candidate.reasons.includes('hidden-ancestor'));
  assert.ok(candidate.reasons.includes('visible-false'));
});
