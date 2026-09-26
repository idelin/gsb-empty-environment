import {
  parseDecimal,
  canonicalDecimal,
  serializeDecimal,
  compareDecimal,
} from '../src/decimal.ts';
import { IncrementalJsonParser, parseJson } from '../src/incremental.ts';
import type { JsonEvent } from '../src/incremental.ts';
import { serializeJson, canonicalJson } from '../src/serialize.ts';
import { validateJson } from '../src/schema.ts';
import { isJsonNumber } from '../src/incremental.ts';

const fmtEvent = (e: JsonEvent): string => {
  if (e.type === 'value' && isJsonNumber(e.value)) {
    return `value number raw=${e.value.decimal.raw} canonical=${canonicalDecimal(e.value.decimal)}`;
  }
  if (e.type === 'value') return `value ${JSON.stringify(e.value)}`;
  if (e.type === 'key') return `key ${JSON.stringify(e.value)}`;
  return e.type;
};

console.log('=== 1. Exact parse: raw lexeme + canonical form ===');
for (const s of ['1e+02', '-0', '9007199254740993', '0.30000000000000000000000001']) {
  const d = parseDecimal(s);
  console.log(
    `raw=${s.padEnd(32)} canonical=${canonicalDecimal(d).padEnd(24)} as-is=${serializeDecimal(d, 'asis')}`,
  );
}
const once = canonicalDecimal(parseDecimal('1e+02'));
console.log(`canonical(1e+02)=${once}, re-canonicalized=${canonicalDecimal(parseDecimal(once))} (idempotent)`);

console.log('\n=== 2. Comparison without expanding huge exponents ===');
console.log('1e100000 < 2e100000            :', compareDecimal(parseDecimal('1e100000'), parseDecimal('2e100000')) < 0);
console.log('9007199254740993 > ...992      :', compareDecimal(parseDecimal('9007199254740993'), parseDecimal('9007199254740992')) > 0, '(Number() would tie)');
console.log('canonical(1e100000)            :', canonicalDecimal(parseDecimal('1e100000')));

console.log('\n=== 3. Incremental parsing, 5-byte chunks ===');
const doc = '{"price": 12.50, "qty": 3, "tag": "A"}';
const parser = new IncrementalJsonParser();
for (let i = 0; i < doc.length; i += 5) {
  for (const e of parser.write(doc.slice(i, i + 5))) {
    console.log(`  chunk[${i}..${Math.min(i + 5, doc.length)}):`, fmtEvent(e));
  }
}
for (const e of parser.end()) console.log('  end:', fmtEvent(e));

console.log('\n=== 4. Unique canonical text for signing vs as-is ===');
const v = parseJson('{"b": 1e+02, "a": [-0, 2.50]}');
console.log('as-is    :', serializeJson(v, { numberMode: 'asis', sortKeys: false }));
console.log('canonical:', canonicalJson(v));

console.log('\n=== 5. Schema subset validation: instance path + rule path ===');
const schema = parseJson(
  '{"type":"object","required":["price"],"additionalProperties":false,' +
    '"properties":{"price":{"type":"number","minimum":0.01,"multipleOf":0.01},' +
    '"qty":{"type":"integer","maximum":9007199254740992}}}',
);
for (const [label, text] of [
  ['valid  ', '{"price": 0.30, "qty": 5}'],
  ['invalid', '{"price": 0.305, "qty": 9007199254740993, "extra": 1}'],
] as const) {
  const errors = validateJson(parseJson(text), schema);
  console.log(`${label} ${text}`);
  if (errors.length === 0) console.log('  -> valid');
  for (const e of errors) {
    console.log(`  -> instance ${e.instancePath || '/'} violates ${e.schemaPath}: ${e.message}`);
  }
}
