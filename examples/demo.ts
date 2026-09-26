import { JsonParser, parseJson } from '../src/parser.ts';
import { serializeJson } from '../src/serializer.ts';
import { validate } from '../src/schema.ts';
import { Decimal, canonicalDecimal, compareDecimal } from '../src/decimal.ts';

const line = (title: string) => console.log(`\n=== ${title} ===`);

line('1. Exact decimals: raw lexeme + exact value');
const doc = parseJson(
  '{"id":9007199254740993,"balance":-0,"rate":1e+02,"tags":["a","b"]}',
) as Record<string, Decimal>;
for (const key of ['id', 'balance', 'rate']) {
  const value = doc[key];
  console.log(
    `  ${key.padEnd(8)} raw=${value.raw.padEnd(18)} canonical=${canonicalDecimal(value)}`,
  );
}
console.log('  9007199254740993 > 9007199254740992 ?',
  compareDecimal(Decimal.fromLexeme('9007199254740993'), Decimal.fromLexeme('9007199254740992')) > 0);

line('2. Serialization: original vs canonical');
console.log('  original :', serializeJson(doc, { numberMode: 'original' }));
console.log('  canonical:', serializeJson(doc, { numberMode: 'canonical' }));

line('3. Incremental parsing: feed arbitrary chunks');
const parser = new JsonParser();
const chunks = ['{"big":900719', '9254740993,"x":1e', '+02}', ''];
for (const chunk of chunks) {
  const events = parser.feed(chunk);
  console.log(`  feed(${JSON.stringify(chunk)}) -> ${events.length} event(s)`);
}
for (const event of parser.end()) {
  if (event.type === 'value') console.log('  end() emitted:', event.value);
}

line('4. Schema subset validation with dual paths');
const schema = {
  type: 'object',
  required: ['id'],
  properties: {
    id: { type: 'integer', minimum: '9007199254740993' },
    rate: { type: 'number', exclusiveMaximum: '1e2' },
  },
};
const result = validate(parseJson('{"id":9007199254740992,"rate":100}'), schema);
console.log('  valid:', result.valid);
for (const error of result.errors) {
  console.log(`  instance=${error.instancePath || '/'} rule=${error.schemaPath} :: ${error.message}`);
}

line('5. Equal values, different lexemes');
for (const raw of ['100', '1e2', '1e+02', '100.0']) {
  const value = Decimal.fromLexeme(raw);
  console.log(`  raw=${raw.padEnd(8)} signature=${value.signature()} (signatures identical, lexemes distinct)`);
}
