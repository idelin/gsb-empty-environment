// Compatibility checker: does the new schema accept every value the old
// schema allows? Rules:
//   R1 requiredness: a field required by the new schema must be guaranteed
//      present by the old schema (old schema requires the same field ID).
//   R2 defaults: an old optional field's default must survive unchanged
//      (same field ID, optional, deep-equal default) -- otherwise messages
//      omitting the field change meaning ("default invalidation").
//   R3 type domains: for every shared field ID, the old type's value domain
//      must be a subset of the new type's (ranges, length bounds, element
//      and record structure, recursively).
// Fields dropped by the new schema are fine (the converter preserves them
// in the unknown area). Field names are display-only.
// On incompatibility a concrete minimal counterexample is produced:
// minimal by total UTF-8 bytes of the canonical JSON encoding, then by
// lexicographic (code-point) order of that encoding.

import { Rec } from './types.ts';
import type { Field, RecordType, TypeNode, Value } from './types.ts';
import { canonical, compareEncoded } from './canonical.ts';
import { minBytesOfLen, minNumberString, minRecordValue, minStringOfLen, minValue } from './values.ts';
import { parseSchema } from './schema.ts';
import { messageToJson } from './convert.ts';

export type CheckResult =
  | { result: 'compatible' }
  | { result: 'incompatible'; path: string; reason: string; counterexample: unknown; encoding: string }
  | { result: 'unsupported'; reason: string };

interface CE {
  value: Value;
  path: string;
  reason: string;
}

export function checkCompat(oldJson: unknown, newJson: unknown): CheckResult {
  const o = parseSchema(oldJson);
  if (!o.ok) return { result: 'unsupported', reason: `old schema: ${o.reason}` };
  const n = parseSchema(newJson);
  if (!n.ok) return { result: 'unsupported', reason: `new schema: ${n.reason}` };
  return checkParsed(o.schema, n.schema);
}

export function checkParsed(oldS: RecordType, newS: RecordType): CheckResult {
  const ce = recordCE(oldS, newS, '');
  if (ce === null) return { result: 'compatible' };
  return {
    result: 'incompatible',
    path: ce.path,
    reason: ce.reason,
    counterexample: messageToJson(ce.value),
    encoding: canonical(ce.value),
  };
}

const seg = (path: string, f: Field) => `${path}.${f.id}(${f.name})`;

function recordCE(oldR: RecordType, newR: RecordType, path: string): CE | null {
  const cands: CE[] = [];
  const oldById = new Map(oldR.fields.map((f) => [f.id, f]));
  const newById = new Map(newR.fields.map((f) => [f.id, f]));

  // R1: required in new, not guaranteed by old -> minimal old-valid message
  // omits the field (it is optional or absent in the old schema).
  for (const nf of newR.fields) {
    if (!nf.required) continue;
    const of = oldById.get(nf.id);
    if (of === undefined || !of.required) {
      cands.push({
        value: minRecordValue(oldR),
        path: seg(path, nf),
        reason: `field ${nf.id} (${nf.name}) is required in the new schema but the old schema does not guarantee its presence`,
      });
    }
  }

  // R2: old default not preserved -> minimal old-valid message omits the
  // field (defaults only exist on optional fields, so omission is valid).
  for (const of of oldR.fields) {
    if (!of.hasDefault) continue;
    const nf = newById.get(of.id);
    if (nf === undefined || nf.required) continue; // dropped: ok; required: covered by R1
    const preserved = nf.hasDefault && canonical(nf.defaultValue!) === canonical(of.defaultValue!);
    if (!preserved) {
      cands.push({
        value: minRecordValue(oldR),
        path: seg(path, of),
        reason: `default of field ${of.id} (${of.name}) is not preserved in the new schema`,
      });
    }
  }

  // R3: shared field type domains.
  for (const of of oldR.fields) {
    const nf = newById.get(of.id);
    if (nf === undefined) continue;
    const inner = typeCE(of.type, nf.type, seg(path, of));
    if (inner !== null) {
      const rec = minRecordValue(oldR);
      rec.fields[String(of.id)] = inner.value;
      cands.push({ value: rec, path: inner.path, reason: inner.reason });
    }
  }

  return pickMin(cands);
}

const b64Length = (n: number) => (n === 0 ? 0 : 4 * Math.ceil(n / 3));

// Minimal bad byte-string value: the bad lengths n are grouped by their
// base64 length (n, n+1, n+2 can share one); minimize the base64 length
// first, then take the lexicographically minimal value within that group.
function minBadBytes(badN: (n: number) => boolean, oldMaxLen: number, path: string, reason: string): CE | null {
  const cands: CE[] = [];
  let groupLen = -1;
  for (let n = 0; n <= oldMaxLen; n++) {
    if (!badN(n)) continue;
    const L = b64Length(n);
    if (groupLen !== -1 && L > groupLen) break; // b64Length is non-decreasing
    groupLen = L;
    cands.push({ value: minBytesOfLen(n), path, reason });
  }
  return pickMin(cands);
}

function pickMin(cands: CE[]): CE | null {
  let best: CE | null = null;
  let bestEnc = '';
  for (const c of cands) {
    const enc = canonical(c.value);
    if (best === null || compareEncoded(enc, bestEnc) < 0) {
      best = c;
      bestEnc = enc;
    }
  }
  return best;
}

type Numeric = { kind: 'int64' | 'uint64'; min: bigint; max: bigint };
const numeric = (t: TypeNode): t is Numeric => t.kind === 'int64' || t.kind === 'uint64';

function typeCE(oldT: TypeNode, newT: TypeNode, path: string): CE | null {
  if (numeric(oldT) && numeric(newT)) {
    // Cross-kind numeric checks reduce to range inclusion, since each
    // range is already validated to lie within its kind's domain.
    return minBadNumber(oldT.min, oldT.max, newT.min, newT.max, path);
  }
  if (oldT.kind === 'string' && newT.kind === 'bytes') {
    // Both are JSON strings at the message level: "" is canonical base64
    // for empty bytes, so only a bounded-empty old string is fully covered.
    if (oldT.maxLen === 0) return null;
    // Canonical base64 has length 0 or >= 4, so every 1-code-point string
    // is invalid; the space is the minimal such string.
    return { value: ' ', path, reason: 'string changed to bytes: non-base64 strings are not accepted' };
  }
  if (oldT.kind === 'bytes' && newT.kind === 'string') {
    // Old values are canonical base64 strings; they violate the new string
    // type only when their base64 length exceeds the new maxLen.
    return minBadBytes((n) => b64Length(n) > newT.maxLen, oldT.maxLen, path,
      `bytes changed to string: base64 length exceeds maxLen ${newT.maxLen}`);
  }
  if (oldT.kind !== newT.kind) {
    return { value: minValue(oldT), path, reason: `type kind changed from ${oldT.kind} to ${newT.kind}` };
  }
  switch (oldT.kind) {
    case 'bool':
      return null;
    case 'string': {
      const n = newT as { kind: 'string'; maxLen: number };
      if (oldT.maxLen <= n.maxLen) return null;
      return {
        value: minStringOfLen(n.maxLen + 1),
        path,
        reason: `string maxLen narrowed from ${oldT.maxLen} to ${n.maxLen}`,
      };
    }
    case 'bytes': {
      const n = newT as { kind: 'bytes'; maxLen: number };
      if (oldT.maxLen <= n.maxLen) return null;
      return minBadBytes((len) => len > n.maxLen, oldT.maxLen, path,
        `bytes maxLen narrowed from ${oldT.maxLen} to ${n.maxLen}`);
    }
    case 'array': {
      const n = newT as { kind: 'array'; maxLen: number; element: TypeNode };
      const cands: CE[] = [];
      if (oldT.maxLen > n.maxLen) {
        cands.push({
          value: Array.from({ length: n.maxLen + 1 }, () => minValue(oldT.element)),
          path,
          reason: `array maxLen narrowed from ${oldT.maxLen} to ${n.maxLen}`,
        });
      }
      if (oldT.maxLen >= 1) {
        const inner = typeCE(oldT.element, n.element, path + '[]');
        if (inner !== null) cands.push({ value: [inner.value], path: inner.path, reason: inner.reason });
      }
      return pickMin(cands);
    }
    case 'record':
      return recordCE(oldT, newT as RecordType, path);
  }
}

function minBadNumber(oldLo: bigint, oldHi: bigint, newLo: bigint, newHi: bigint, path: string): CE | null {
  const cands: CE[] = [];
  const reason = `integer range narrowed from [${oldLo}, ${oldHi}] to [${newLo}, ${newHi}]`;
  if (oldLo < newLo) {
    const hi = oldHi < newLo - 1n ? oldHi : newLo - 1n;
    cands.push({ value: BigInt(minNumberString(oldLo, hi)), path, reason });
  }
  if (oldHi > newHi) {
    const lo = oldLo > newHi + 1n ? oldLo : newHi + 1n;
    cands.push({ value: BigInt(minNumberString(lo, oldHi)), path, reason });
  }
  return pickMin(cands);
}
