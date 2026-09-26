import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal, canonicalDecimal } from '../src/decimal.ts';
import { JsonParseError, JsonParser, parseJson } from '../src/parser.ts';
import type { JsonValue, ParseEvent } from '../src/parser.ts';
import { serializeJson } from '../src/serializer.ts';

const DOC =
  '{"big":9007199254740993,"neg":-0,"sci":1e+02,"arr":[1,2.5e3,-1.5e-7,"xA😀"],' +
  '"obj":{"a":null,"b":true,"c":false},"s":"hi\\n\\u0041"}';

function runChunked(chunks: Array<string | Uint8Array>): ParseEvent[] {
  const parser = new JsonParser();
  const events: ParseEvent[] = [];
  for (const chunk of chunks) events.push(...parser.feed(chunk));
  events.push(...parser.end());
  return events;
}

function eventSnapshot(events: ParseEvent[]): string {
  return JSON.stringify(events, (_key, value) => {
    if (value instanceof Decimal) {
      return { decimal: canonicalDecimal(value), raw: value.raw };
    }
    return value;
  });
}

test('chunked parsing is consistent with whole-input parsing at every split point', () => {
  const whole = eventSnapshot(runChunked([DOC]));
  for (let cut = 0; cut <= DOC.length; cut++) {
    const events = runChunked([DOC.slice(0, cut), DOC.slice(cut)]);
    assert.equal(eventSnapshot(events), whole, `split at ${cut}`);
  }
});

test('chunked parsing is consistent for seeded random multi-splits', () => {
  const whole = eventSnapshot(runChunked([DOC]));
  let seed = 0x2f6e2b1;
  const random = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };
  for (let trial = 0; trial < 200; trial++) {
    const cuts = new Set<number>();
    const count = 1 + Math.floor(random() * 6);
    for (let k = 0; k < count; k++) cuts.add(Math.floor(random() * (DOC.length + 1)));
    const sorted = [...cuts].sort((a, b) => a - b);
    const chunks: string[] = [];
    let prev = 0;
    for (const cut of sorted) {
      chunks.push(DOC.slice(prev, cut));
      prev = cut;
    }
    chunks.push(DOC.slice(prev));
    assert.equal(eventSnapshot(runChunked(chunks)), whole, `chunks ${JSON.stringify(sorted)}`);
  }
});

test('byte-wise feeding (one UTF-8 byte at a time) matches whole parsing', () => {
  const bytes = new TextEncoder().encode(DOC);
  const whole = eventSnapshot(runChunked([DOC]));
  const parser = new JsonParser();
  const events: ParseEvent[] = [];
  for (const byte of bytes) events.push(...parser.feed(new Uint8Array([byte])));
  events.push(...parser.end());
  assert.equal(eventSnapshot(events), whole);
});

test('a paused parser never publishes half a number', () => {
  const parser = new JsonParser();
  const noValues = (events: ParseEvent[]) => events.filter((e) => e.type === 'value');
  assert.deepEqual(noValues(parser.feed('[12')), []);
  assert.deepEqual(noValues(parser.feed('3')), []);
  assert.deepEqual(noValues(parser.feed('.')), []);
  assert.deepEqual(noValues(parser.feed('4e')), []);
  const events = parser.feed('1,5]');
  const numbers = events.filter((e) => e.type === 'value');
  assert.equal(numbers.length, 2);
  const first = numbers[0] as Extract<ParseEvent, { type: 'value' }>;
  assert.ok(first.value instanceof Decimal);
  assert.equal((first.value as Decimal).raw, '123.4e1');
  assert.equal(canonicalDecimal(first.value as Decimal), '1234');
});

test('top-level number completes only at end()', () => {
  const parser = new JsonParser();
  assert.deepEqual(parser.feed('12'), []);
  assert.deepEqual(parser.feed('3'), []);
  const events = parser.end();
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'value');
  assert.equal((events[0] as { value: Decimal }).value.raw, '123');
});

test('numbers keep raw lexeme and exact value through the DOM', () => {
  const doc = parseJson(DOC) as { [key: string]: JsonValue };
  const big = doc.big as Decimal;
  assert.equal(big.raw, '9007199254740993');
  assert.equal(big.digits, '9007199254740993');
  const neg = doc.neg as Decimal;
  assert.equal(neg.raw, '-0');
  assert.equal(neg.negative, true);
  const sci = doc.sci as Decimal;
  assert.equal(sci.raw, '1e+02');
  assert.equal(canonicalDecimal(sci), '100');
});

test('parse errors: leading zero, non-finite literals, truncated numbers', () => {
  const bad = ['[01]', '[-01]', '[00]', 'Infinity', 'NaN', '-Infinity', '[1e]', '[1.]', '[+1]', '[1e+]', '01'];
  for (const text of bad) {
    assert.throws(() => parseJson(text), JsonParseError, JSON.stringify(text));
  }
  const error = catchError(() => parseJson('[01]'));
  assert.match(error.message, /invalid JSON number lexeme/);
  assert.match(error.message, /offset 1/);
});

test('parse errors: structural problems report offsets', () => {
  assert.throws(() => parseJson(''), /empty/);
  assert.throws(() => parseJson('[1,'), /unclosed array|unexpected end/);
  assert.throws(() => parseJson('{"a":1,'), /unclosed object|unexpected end/);
  assert.throws(() => parseJson('[1,]'), /unexpected/);
  assert.throws(() => parseJson('{"a":1,}'), /unexpected/);
  assert.throws(() => parseJson('1 2'), /after JSON root/);
  assert.throws(() => parseJson('nul'), /unterminated literal|unexpected end/);
  assert.throws(() => parseJson('nulx'), /invalid literal/);
  assert.throws(() => parseJson('"abc'), /unterminated string|unexpected end/);
  assert.throws(() => parseJson('"\\u12"'), /invalid \\u hexadecimal escape/);
  assert.throws(() => parseJson('"\\q"'), /invalid escape/);
});

test('surrogate pairs combine across chunk boundaries', () => {
  const parser = new JsonParser();
  const events: ParseEvent[] = [];
  events.push(...parser.feed('"\\uD83'));
  events.push(...parser.feed('D\\uDE00"'));
  events.push(...parser.end());
  const valueEvent = events.find((e) => e.type === 'value');
  assert.equal((valueEvent as { value: string }).value, '😀');
  assert.throws(() => parseJson('"\\uD800x"'), /low surrogate/);
  assert.throws(() => parseJson('"\\uDC00"'), /unpaired low surrogate/);
});

test('multi-byte UTF-8 split across byte chunks', () => {
  const bytes = new TextEncoder().encode('["é😀"]');
  const parser = new JsonParser();
  const events: ParseEvent[] = [];
  for (let i = 0; i < bytes.length; i += 1) {
    events.push(...parser.feed(bytes.subarray(i, i + 1)));
  }
  events.push(...parser.end());
  const valueEvent = events.find((e) => e.type === 'value');
  assert.equal((valueEvent as { value: string }).value, 'é😀');
});

test('serialize round-trips: original mode preserves lexemes, canonical normalizes', () => {
  const doc = parseJson(DOC);
  const original = serializeJson(doc, { numberMode: 'original' });
  const reparsed = parseJson(original);
  assert.equal(serializeJson(reparsed, { numberMode: 'original' }), original);
  assert.ok(original.includes('9007199254740993'));
  assert.ok(original.includes('-0'));
  assert.ok(original.includes('1e+02'));

  const canonical = serializeJson(doc, { numberMode: 'canonical' });
  assert.equal(canonical, serializeJson(parseJson(canonical), { numberMode: 'canonical' }),
    'canonical serialization is a fixpoint');
  assert.ok(canonical.includes('"neg":0'), 'negative zero normalizes to 0');
  assert.ok(canonical.includes('"sci":100'), '1e+02 normalizes to 100');
});

function catchError(fn: () => unknown): Error {
  try {
    fn();
  } catch (error) {
    return error as Error;
  }
  throw new Error('expected function to throw');
}
