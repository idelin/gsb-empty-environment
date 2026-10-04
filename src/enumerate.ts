// Exhaustive verification over finite small domains. Enumerates every
// message allowed by the old schema (within the given caps) and checks the
// compatibility predicate directly: conversion must succeed and old
// defaults must be preserved. The result (and the minimal counterexample)
// is compared against the checker's output.
//
// The enumeration domain is chosen so the checker's minimal
// counterexamples are always inside it: the string alphabet contains
// U+0020 (the minimal unescaped 1-byte character) and the byte alphabet
// contains every byte of minBytesOfLen(n) for n <= maxLen+1.

import { Rec } from './types.ts';
import type { Field, RecordType, TypeNode, Value } from './types.ts';
import { canonical, compareEncoded } from './canonical.ts';
import { parseSchema } from './schema.ts';
import { checkParsed } from './check.ts';
import type { CheckResult } from './check.ts';
import { convertMessage } from './convert.ts';
import { minBytesOfLen, minRecordValue } from './values.ts';

export interface Caps {
  maxLen: number; // enumeration cap for string/bytes/array lengths
  maxValues: number; // cap on values per type node
  maxMessages: number; // cap on total enumerated messages
}

export const DEFAULT_CAPS: Caps = { maxLen: 2, maxValues: 500, maxMessages: 100000 };

class TooBig extends Error {}

const ABSENT: unique symbol = Symbol('absent');
type Maybe = Value | typeof ABSENT;

export function stringAlphabet(): string[] {
  return [' ', 'a', 'b'];
}

export function byteAlphabet(caps: Caps): number[] {
  const set = new Set<number>([0x00, 0xff]);
  for (let n = 0; n <= caps.maxLen + 1; n++) {
    for (const b of minBytesOfLen(n)) set.add(b);
  }
  return [...set].sort((a, b) => a - b);
}

function geomSum(base: number, n: number): number {
  if (base === 0) return 1;
  if (base === 1) return n + 1;
  return (base ** (n + 1) - 1) / (base - 1);
}

export function enumerateValues(t: TypeNode, caps: Caps): Value[] {
  switch (t.kind) {
    case 'bool':
      return [false, true];
    case 'int64':
    case 'uint64': {
      const count = Number(t.max - t.min) + 1;
      if (count > caps.maxValues) throw new TooBig('int range');
      const out: Value[] = [];
      for (let i = t.min; i <= t.max; i++) out.push(i);
      return out;
    }
    case 'string': {
      const alphabet = stringAlphabet();
      const maxLen = Math.min(t.maxLen, caps.maxLen);
      if (geomSum(alphabet.length, maxLen) > caps.maxValues) throw new TooBig('string');
      const out: Value[] = [];
      const build = (prefix: string) => {
        out.push(prefix);
        if (prefix.length < maxLen) for (const ch of alphabet) build(prefix + ch);
      };
      build('');
      return out;
    }
    case 'bytes': {
      const alphabet = byteAlphabet(caps);
      const maxLen = Math.min(t.maxLen, caps.maxLen);
      if (geomSum(alphabet.length, maxLen) > caps.maxValues) throw new TooBig('bytes');
      const out: Value[] = [];
      const build = (prefix: number[]) => {
        out.push(Uint8Array.from(prefix));
        if (prefix.length < maxLen) for (const b of alphabet) build([...prefix, b]);
      };
      build([]);
      return out;
    }
    case 'array': {
      const els = enumerateValues(t.element, caps);
      const maxLen = Math.min(t.maxLen, caps.maxLen);
      if (geomSum(els.length, maxLen) > caps.maxValues) throw new TooBig('array');
      const out: Value[] = [];
      const build = (prefix: Value[]) => {
        out.push([...prefix]);
        if (prefix.length < maxLen) for (const el of els) build([...prefix, el]);
      };
      build([]);
      return out;
    }
    case 'record': {
      const perField: { f: Field; vals: Maybe[] }[] = t.fields.map((f) => {
        const vals = enumerateValues(f.type, caps);
        return { f, vals: f.required ? vals : [ABSENT, ...vals] };
      });
      let total = 1;
      for (const p of perField) total *= p.vals.length;
      if (total > caps.maxMessages) throw new TooBig('record');
      const out: Value[] = [];
      const build = (i: number, acc: Record<string, Value>) => {
        if (i === perField.length) {
          out.push(new Rec({ ...acc }, {}));
          return;
        }
        const { f, vals } = perField[i];
        for (const v of vals) {
          if (v === ABSENT) delete acc[String(f.id)];
          else acc[String(f.id)] = v;
          build(i + 1, acc);
        }
        delete acc[String(f.id)];
      };
      build(0, {});
      return out;
    }
  }
}

export type VerifyResult =
  | { result: 'match'; checker: CheckResult; messageCount: number }
  | { result: 'mismatch'; checker: CheckResult; bruteForce: unknown; messageCount: number }
  | { result: 'too-big' }
  | { result: 'unsupported'; reason: string };

export function verifyExhaustive(oldJson: unknown, newJson: unknown, caps: Caps = DEFAULT_CAPS): VerifyResult {
  const o = parseSchema(oldJson);
  if (!o.ok) return { result: 'unsupported', reason: `old schema: ${o.reason}` };
  const n = parseSchema(newJson);
  if (!n.ok) return { result: 'unsupported', reason: `new schema: ${n.reason}` };
  const checker = checkParsed(o.schema, n.schema);

  let messages: Value[];
  try {
    messages = enumerateValues(o.schema, caps);
  } catch (e) {
    if (e instanceof TooBig) return { result: 'too-big' };
    throw e;
  }
  if (messages.length > caps.maxMessages) return { result: 'too-big' };

  // Brute-force predicate, independent of the checker's analysis:
  // (a) every old-valid message must convert successfully;
  // (b) every old default must be preserved by the new schema.
  const newById = new Map(n.schema.fields.map((f) => [f.id, f]));
  let defaultBroken = false;
  for (const of of o.schema.fields) {
    if (!of.hasDefault) continue;
    const nf = newById.get(of.id);
    if (nf === undefined || nf.required) continue;
    if (!nf.hasDefault || canonical(nf.defaultValue!) !== canonical(of.defaultValue!)) defaultBroken = true;
  }

  const failing: Value[] = [];
  for (const m of messages) {
    if (!convertMessage(o.schema, n.schema, m).ok) failing.push(m);
  }
  if (defaultBroken) failing.push(minRecordValue(o.schema));

  let minEnc: string | null = null;
  for (const m of failing) {
    const enc = canonical(m);
    if (minEnc === null || compareEncoded(enc, minEnc) < 0) minEnc = enc;
  }

  const bruteCompatible = minEnc === null;
  if (bruteCompatible && checker.result === 'compatible') {
    return { result: 'match', checker, messageCount: messages.length };
  }
  if (!bruteCompatible && checker.result === 'incompatible' && checker.encoding === minEnc) {
    return { result: 'match', checker, messageCount: messages.length };
  }
  return {
    result: 'mismatch',
    checker,
    bruteForce: bruteCompatible ? { compatible: true } : { compatible: false, minimalEncoding: minEnc },
    messageCount: messages.length,
  };
}
