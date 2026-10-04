// Finite-domain exhaustive verifier.
//
// Enumerates every old-schema-valid value inside a finite domain (small
// integer ranges in full; strings/bytes over a configurable alphabet up
// to their length cap; arrays/records as products) and cross-checks the
// checker's verdict:
//   - "compatible"   => no enumerated value may violate the new schema
//   - "incompatible" => the reported counterexample must be old-valid,
//                       new-invalid, and <= every enumerated counterexample
//                       under the (size, canonical-lexicographic) order
// When the domain is fully enumerated (`exhaustive: true`) this proves
// the verdict and the counterexample's minimality within that domain.

import { check } from './check.ts';
import type { CheckResult } from './check.ts';
import type { TypeNode, Value } from './types.ts';
import { parseSchema } from './validate.ts';
import {
  absentAccepted,
  compareValues,
  isValid,
  jsonToValue,
  minValue,
  valueToJson,
} from './value.ts';

export interface VerifyDomain {
  stringAlphabet?: string[]; // default: [' ', 'a', NUL, 'é', '😀']
  byteAlphabet?: number[]; // default: [0x00, 0x01, 0xff]
  valueCap?: number; // max enumerated values per type node (default 20000)
}

export interface VerifyResult {
  checkResult: CheckResult['result'];
  exhaustive: boolean;
  enumerated: number;
  consistent: boolean;
  problems: string[];
  minimalEnumeratedCounterexample?: unknown;
}

const NUL = String.fromCharCode(0);
const DEFAULT_STRING_ALPHABET = [' ', 'a', NUL, 'é', '😀'];
const DEFAULT_BYTE_ALPHABET = [0x00, 0x01, 0xff];

interface EnumResult {
  values: Value[];
  exhaustive: boolean;
}

export function verify(oldRaw: unknown, newRaw: unknown, domain: VerifyDomain = {}): VerifyResult {
  const result = check(oldRaw, newRaw);
  const problems: string[] = [];
  if (result.result === 'unsupported') {
    return {
      checkResult: 'unsupported',
      exhaustive: false,
      enumerated: 0,
      consistent: false,
      problems: ['checker returned unsupported; nothing to verify', ...result.reasons],
    };
  }
  const oldSchema = parseSchema(oldRaw).schema!;
  const newSchema = parseSchema(newRaw).schema!;

  const alphabet = domain.stringAlphabet ?? DEFAULT_STRING_ALPHABET;
  const byteAlphabet = domain.byteAlphabet ?? DEFAULT_BYTE_ALPHABET;
  const cap = domain.valueCap ?? 20000;

  const enumd = enumerate(oldSchema.root, alphabet, byteAlphabet, cap);
  const counterexamples: Value[] = [];
  for (const v of enumd.values) {
    if (!isValid(v, oldSchema.root)) {
      problems.push(`enumerator produced old-invalid value: ${JSON.stringify(valueToJson(v))}`);
      continue;
    }
    if (!isValid(v, newSchema.root)) counterexamples.push(v);
  }

  let consistent = problems.length === 0;
  let minimalEnumerated: Value | undefined;

  if (result.result === 'compatible') {
    if (counterexamples.length > 0) {
      consistent = false;
      problems.push(
        `checker said compatible but ${counterexamples.length} enumerated value(s) violate the new schema, e.g. ` +
          JSON.stringify(valueToJson(counterexamples[0])),
      );
    }
  } else {
    // incompatible: validate the reported counterexample
    let cexValue: Value | null = null;
    try {
      cexValue = jsonToValue(result.counterexample, oldSchema.root);
    } catch (e) {
      consistent = false;
      problems.push(`counterexample does not parse against old schema: ${(e as Error).message}`);
    }
    if (cexValue) {
      if (!isValid(cexValue, oldSchema.root)) {
        consistent = false;
        problems.push('counterexample is not valid under the old schema');
      }
      if (isValid(cexValue, newSchema.root)) {
        consistent = false;
        problems.push('counterexample is valid under the new schema (not a witness)');
      }
    }
    if (counterexamples.length === 0) {
      consistent = false;
      problems.push('checker said incompatible but no enumerated value violates the new schema');
    } else {
      minimalEnumerated = counterexamples.reduce((a, b) => (compareValues(a, b) <= 0 ? a : b));
      if (cexValue && compareValues(cexValue, minimalEnumerated) > 0) {
        consistent = false;
        problems.push(
          `counterexample is not minimal: enumerated ${JSON.stringify(valueToJson(minimalEnumerated))} is smaller`,
        );
      }
    }
  }

  return {
    checkResult: result.result,
    exhaustive: enumd.exhaustive,
    enumerated: enumd.values.length,
    consistent,
    problems,
    ...(minimalEnumerated !== undefined
      ? { minimalEnumeratedCounterexample: valueToJson(minimalEnumerated) }
      : {}),
  };
}

// ---------------------------------------------------------------------------
// Enumeration
// ---------------------------------------------------------------------------

export function enumerate(
  t: TypeNode,
  alphabet: string[] = DEFAULT_STRING_ALPHABET,
  byteAlphabet: number[] = DEFAULT_BYTE_ALPHABET,
  cap: number = 20000,
): EnumResult {
  switch (t.kind) {
    case 'bool':
      return {
        values: [
          { kind: 'bool', value: false },
          { kind: 'bool', value: true },
        ],
        exhaustive: true,
      };

    case 'int64':
    case 'uint64': {
      const count = t.max - t.min + 1n;
      if (count <= BigInt(cap)) {
        const values: Value[] = [];
        for (let v = t.min; v <= t.max; v++) values.push({ kind: 'int', value: v });
        return { values, exhaustive: true };
      }
      // Boundary sample for huge domains.
      const sample = new Set<bigint>();
      const add = (v: bigint): void => {
        if (v >= t.min && v <= t.max) sample.add(v);
      };
      add(t.min);
      add(t.min + 1n);
      add(t.max - 1n);
      add(t.max);
      add(0n);
      add(-1n);
      add(1n);
      return {
        values: [...sample].map((v) => ({ kind: 'int', value: v }) as Value),
        exhaustive: false,
      };
    }

    case 'string': {
      const total = Math.pow(alphabet.length, t.maxLength + 1); // upper bound of the sum
      if (total <= cap + 1) {
        const values: Value[] = [];
        const build = (prefix: string, remaining: number): void => {
          values.push({ kind: 'string', value: prefix });
          if (remaining === 0) return;
          for (const ch of alphabet) build(prefix + ch, remaining - 1);
        };
        build('', t.maxLength);
        return { values, exhaustive: true };
      }
      // Sampled: repeated-char strings at every length plus edge mixes.
      const values: Value[] = [];
      for (let len = 0; len <= t.maxLength; len++) {
        for (const ch of alphabet) values.push({ kind: 'string', value: ch.repeat(len) });
        if (len >= 2) {
          values.push({ kind: 'string', value: alphabet[0].repeat(len - 1) + alphabet[1] });
          values.push({ kind: 'string', value: alphabet[1] + alphabet[0].repeat(len - 1) });
        }
      }
      return { values, exhaustive: false };
    }

    case 'bytes': {
      const total = Math.pow(byteAlphabet.length, t.maxLength + 1);
      if (total <= cap + 1) {
        const values: Value[] = [];
        const build = (prefix: number[], remaining: number): void => {
          values.push({ kind: 'bytes', value: Uint8Array.from(prefix) });
          if (remaining === 0) return;
          for (const b of byteAlphabet) build([...prefix, b], remaining - 1);
        };
        build([], t.maxLength);
        return { values, exhaustive: true };
      }
      const values: Value[] = [];
      for (let len = 0; len <= t.maxLength; len++) {
        for (const b of byteAlphabet) {
          values.push({ kind: 'bytes', value: Uint8Array.from(Array(len).fill(b)) });
        }
      }
      return { values, exhaustive: false };
    }

    case 'array': {
      const itemEnum = enumerate(t.items, alphabet, byteAlphabet, cap);
      const itemCount = itemEnum.values.length;
      let total = 0;
      for (let len = t.minLength; len <= t.maxLength; len++) total += Math.pow(itemCount, len);
      if (total <= cap) {
        const values: Value[] = [];
        const build = (prefix: Value[], remaining: number): void => {
          if (remaining === 0) {
            values.push({ kind: 'array', items: [...prefix] });
            return;
          }
          for (const item of itemEnum.values) build([...prefix, item], remaining - 1);
        };
        for (let len = t.minLength; len <= t.maxLength; len++) build([], len);
        return { values, exhaustive: itemEnum.exhaustive };
      }
      // Sampled: min arrays at every length plus each item placed at the
      // first and last position of the shortest length.
      const values: Value[] = [];
      const minItem = minValue(t.items);
      for (let len = t.minLength; len <= t.maxLength; len++) {
        values.push({ kind: 'array', items: Array.from({ length: len }, () => minItem) });
      }
      const len = Math.max(t.minLength, 1);
      if (len <= t.maxLength) {
        for (const item of itemEnum.values) {
          const first = Array.from({ length: len }, () => minItem);
          first[0] = item;
          values.push({ kind: 'array', items: first });
          const last = Array.from({ length: len }, () => minItem);
          last[len - 1] = item;
          values.push({ kind: 'array', items: last });
        }
      }
      return { values, exhaustive: false };
    }

    case 'record': {
      const options: { id: number; choices: (Value | 'absent')[] }[] = [];
      let total = 1;
      let exhaustive = true;
      let overflow = false;
      for (const f of t.fields) {
        const sub = enumerate(f.type, alphabet, byteAlphabet, cap);
        exhaustive = exhaustive && sub.exhaustive;
        const choices: (Value | 'absent')[] = [...sub.values];
        if (absentAccepted(f)) choices.push('absent');
        total *= choices.length;
        options.push({ id: f.id, choices });
        if (total > cap) {
          overflow = true;
          break;
        }
      }
      if (!overflow) {
        const values: Value[] = [];
        const build = (i: number, acc: [number, Value][]): void => {
          if (i === options.length) {
            values.push({ kind: 'record', fields: [...acc], unknown: [] });
            return;
          }
          for (const choice of options[i].choices) {
            if (choice === 'absent') build(i + 1, acc);
            else build(i + 1, [...acc, [options[i].id, choice]]);
          }
        };
        build(0, []);
        return { values, exhaustive };
      }
      // Sampled: the minimal record plus each field varied over its domain.
      const values: Value[] = [];
      const base = minValue(t) as {
        kind: 'record';
        fields: [number, Value][];
        unknown: [string, unknown][];
      };
      values.push(base);
      for (const f of t.fields) {
        const sub = enumerate(f.type, alphabet, byteAlphabet, cap);
        const baseMap = new Map(base.fields);
        for (const val of sub.values) {
          const fields = new Map(baseMap);
          fields.set(f.id, val);
          values.push({ kind: 'record', fields: [...fields], unknown: [] });
        }
        if (absentAccepted(f)) {
          const fields = new Map(baseMap);
          fields.delete(f.id);
          values.push({ kind: 'record', fields: [...fields], unknown: [] });
        }
      }
      return { values, exhaustive: false };
    }
  }
}
