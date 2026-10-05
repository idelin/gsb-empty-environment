#!/usr/bin/env node
// CLI for the consensus trace checker.
//
//   node src/cli.ts check  <trace.json> [--budget N]
//   node src/cli.ts verify <evidence.json>
//
// trace.json:    { "members": ["n1",..,"n5"], "events": [...] }
//                (a bare event array is also accepted; members are derived)
// evidence.json: { "claim": {...}, "events": [...] }
//
// Exit codes: check  -> 0 no violation, 1 violation(s) found, 2 bad input
//             verify -> 0 evidence valid, 1 evidence invalid, 2 bad input

import { readFileSync } from 'node:fs';
import { checkTrace } from './checker.ts';
import { verifyEvidence } from './verifier.ts';
import { validateEvent, TraceConflictError, TraceValidationError } from './trace.ts';
import { MEMBER_COUNT } from './types.ts';
import type { TraceEvent, ViolationClaim } from './types.ts';

function usage(): never {
  console.error(`usage:
  node src/cli.ts check  <trace.json> [--budget N]
  node src/cli.ts verify <evidence.json>`);
  process.exit(2);
}

function readJson(file: string): unknown {
  try {
    return JSON.parse(readFileSync(file, 'utf8'));
  } catch (err) {
    console.error(JSON.stringify({ error: `cannot read ${file}: ${(err as Error).message}` }));
    process.exit(2);
  }
}

function cmdCheck(file: string, rest: string[]): void {
  let budget: number | undefined;
  for (let i = 0; i < rest.length; i++) {
    if (rest[i] === '--budget') {
      budget = Number(rest[++i]);
      if (!Number.isInteger(budget) || budget < 1) {
        console.error(JSON.stringify({ error: '--budget must be a positive integer' }));
        process.exit(2);
      }
    } else {
      usage();
    }
  }

  const doc = readJson(file) as { members?: string[]; events?: unknown[] } | unknown[];
  let members: string[];
  let events: unknown[];
  if (Array.isArray(doc)) {
    events = doc;
    members = [
      ...new Set(
        (doc as { node?: unknown }[]).map((e) => e?.node).filter((n): n is string => typeof n === 'string'),
      ),
    ].sort();
    if (members.length !== MEMBER_COUNT) {
      console.error(
        JSON.stringify({ error: `trace must reference exactly ${MEMBER_COUNT} distinct members, got ${members.length}` }),
      );
      process.exit(2);
    }
  } else if (doc && Array.isArray(doc.events) && Array.isArray(doc.members)) {
    members = doc.members;
    events = doc.events;
  } else {
    console.error(JSON.stringify({ error: 'trace file must be an event array or {members, events}' }));
    process.exit(2);
  }

  try {
    const result = checkTrace(events, members, { budget });
    console.log(JSON.stringify(result, null, 2));
    process.exit(result.ok ? 0 : 1);
  } catch (err) {
    if (err instanceof TraceConflictError || err instanceof TraceValidationError) {
      console.error(JSON.stringify({ error: err.message }));
      process.exit(2);
    }
    throw err;
  }
}

function cmdVerify(file: string): void {
  const doc = readJson(file) as { claim?: ViolationClaim; events?: unknown[] };
  if (!doc || typeof doc !== 'object' || !doc.claim || !Array.isArray(doc.events)) {
    console.error(JSON.stringify({ error: 'evidence file must be {claim, events}' }));
    process.exit(2);
  }
  let events: TraceEvent[];
  try {
    const nodes = new Set(
      (doc.events as { node?: unknown }[]).map((e) => e?.node).filter((n): n is string => typeof n === 'string'),
    );
    events = doc.events.map((e) => validateEvent(e, nodes));
  } catch (err) {
    if (err instanceof TraceValidationError) {
      console.error(JSON.stringify({ error: err.message }));
      process.exit(2);
    }
    throw err;
  }
  const valid = verifyEvidence(events, doc.claim);
  console.log(JSON.stringify({ valid, claim: doc.claim, eventIds: events.map((e) => e.id) }, null, 2));
  process.exit(valid ? 0 : 1);
}

const [, , cmd, file, ...rest] = process.argv;
if (!cmd || !file) usage();
if (cmd === 'check') cmdCheck(file, rest);
else if (cmd === 'verify') cmdVerify(file);
else usage();
