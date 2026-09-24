import {
  CheckpointMismatchError,
  IncrementalUtf8Decoder,
  Utf8DecodeError,
  type DecodeEvent,
} from '../src/index.ts';

const enc = new TextEncoder();
const line = (s = ''): void => console.log(s);

// ---------------------------------------------------------------------------
line('=== 1. Incremental decode with replace strategy (chunk size 3) ===');
const parts = ['Aé', '€😀', 'xy\r\nz\n'];
const head = parts.map((p) => enc.encode(p));
const invalid = Uint8Array.of(0xe2, 0x82); // truncated 3-byte sequence
const bytes = new Uint8Array(head.reduce((n, b) => n + b.length, 0) + invalid.length);
{
  let p = 0;
  for (const b of [...head, invalid]) {
    bytes.set(b, p);
    p += b.length;
  }
}
line(`input bytes : [${[...bytes].map((b) => b.toString(16).padStart(2, '0')).join(' ')}]`);

const decoder = new IncrementalUtf8Decoder('replace');
const events: DecodeEvent[] = [];
for (let p = 0; p < bytes.length; p += 3) {
  events.push(...decoder.push(bytes.subarray(p, p + 3)));
}
events.push(...decoder.finish());

line('kind         bytes       utf16   text');
for (const e of events) {
  line(
    `${e.kind.padEnd(12)} [${String(e.byteStart).padStart(2)},${String(e.byteEnd).padStart(3)})  ` +
      `${String(e.u16Start).padStart(2)}+${e.u16Length}  ${JSON.stringify(e.text)}`,
  );
}
line(`decoded text: ${JSON.stringify(events.map((e) => e.text).join(''))}`);

// ---------------------------------------------------------------------------
line();
line('=== 2. Offset queries (UTF-16 space, surrogate-safe) ===');
const map = decoder.map;
line(`total: ${map.byteLength} bytes, ${map.u16Length} utf16 code units, ${map.runCount} compressed runs`);
line(`byteToU16(7)  = ${map.byteToU16(7)}   (inside the 4-byte emoji at bytes 6..10)`);
line(`u16ToByte(4)  = ${map.u16ToByte(4)}   (middle of the emoji surrogate pair -> snaps to pair start)`);
const range = map.byteRangeToU16(15, 18);
line(`byteRangeToU16(15, 18) -> utf16 [${range.start}, ${range.end})`);
for (const r of range.replacements) {
  line(`  many-to-one coverage: bytes [${r.byteStart}, ${r.byteEnd}) -> utf16 [${r.u16Start}, ${r.u16End})`);
}
const back = map.u16RangeToByte(3, 4);
line(`u16RangeToByte(3, 4) -> bytes [${back.start}, ${back.end})  (snapped outward, pair not split)`);

// ---------------------------------------------------------------------------
line();
line('=== 3. Serializable checkpoint, bound to the input prefix digest ===');
const d1 = new IncrementalUtf8Decoder('replace');
d1.push(bytes.subarray(0, 8)); // stops mid-emoji
const checkpoint = d1.checkpoint();
const serialized = JSON.stringify(checkpoint);
line(`checkpoint JSON (${serialized.length} chars): ${serialized.slice(0, 110)}...`);
line(`prefixDigest: ${checkpoint.prefixDigest}`);

const revived = IncrementalUtf8Decoder.resume(JSON.parse(serialized), bytes.subarray(0, 8));
const rest = [...revived.push(bytes.subarray(8)), ...revived.finish()];
line(`resumed decode of remaining ${bytes.length - 8} bytes: ${JSON.stringify(rest.map((e) => e.text).join(''))}`);

try {
  IncrementalUtf8Decoder.resume(JSON.parse(serialized), enc.encode('12345678'));
} catch (e) {
  line(`resume with a different stream rejected: ${(e as CheckpointMismatchError).message}`);
}

// ---------------------------------------------------------------------------
line();
line('=== 4. Strict strategy: absolute error positions ===');
const strict = new IncrementalUtf8Decoder('strict');
strict.push(enc.encode('hello'));
try {
  strict.push(Uint8Array.of(0xc3, 0x28)); // 0x28 is not a continuation byte
} catch (e) {
  const err = e as Utf8DecodeError;
  line(`threw ${err.name}: kind=${err.kind} byteOffset=${err.byteOffset} sequence=[${[...err.sequence].map((b) => '0x' + b.toString(16))}]`);
}
const strict2 = new IncrementalUtf8Decoder('strict');
strict2.push(Uint8Array.of(0xf0, 0x9f)); // incomplete emoji
try {
  strict2.finish();
} catch (e) {
  const err = e as Utf8DecodeError;
  line(`finish() threw ${err.name}: kind=${err.kind} byteOffset=${err.byteOffset} (truncated input is only an error at finish)`);
}
