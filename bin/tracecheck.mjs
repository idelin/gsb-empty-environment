#!/usr/bin/env node
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkTrace, verifyEvidence, samples, MEMBERS, QUORUM } from '../dist/index.js';

const args = process.argv.slice(2);
const cmd = args[0];

const USAGE = [
  'tracecheck - safety trace checker for the fixed 5-member consensus (' + MEMBERS.join(', ') + ', quorum ' + QUORUM + ')',
  '',
  'usage:',
  '  tracecheck check <trace.json> [--budget N] [--emit-evidence DIR]',
  '  tracecheck verify <evidence.json>',
  '  tracecheck samples [dir]',
  '',
  'exit codes (check): 0 ok, 1 safety violations, 2 rejected trace / usage error, 4 incomplete evidence',
  'exit codes (verify): 0 evidence proves the claim, 1 it does not',
].join('\n');

function fail(msg) {
  console.error('error: ' + msg);
  console.error(USAGE);
  process.exit(2);
}

function readJson(file) {
  try {
    return JSON.parse(readFileSync(file, 'utf8'));
  } catch (err) {
    fail('cannot read ' + file + ': ' + err.message);
  }
}

if (cmd === 'check') {
  const file = args[1];
  if (!file || file.startsWith('--')) fail('missing trace file');
  let budget = 50000;
  let emitDir = null;
  for (let i = 2; i < args.length; i++) {
    if (args[i] === '--budget') {
      budget = Number(args[++i]);
      if (!Number.isInteger(budget) || budget < 0) fail('--budget must be a non-negative integer');
    } else if (args[i] === '--emit-evidence') {
      emitDir = args[++i];
      if (!emitDir) fail('--emit-evidence needs a directory');
    } else {
      fail('unknown option ' + args[i]);
    }
  }
  const trace = readJson(file);
  const events = Array.isArray(trace) ? trace : trace.events;
  const report = checkTrace(events, { budget });
  console.log(JSON.stringify(report, null, 2));
  if (emitDir) {
    mkdirSync(emitDir, { recursive: true });
    report.violations.forEach((v, i) => {
      const name = 'evidence-' + i + '-' + v.kind + '.json';
      writeFileSync(join(emitDir, name), JSON.stringify({ claim: v.claim, events: v.evidence.events }, null, 2));
    });
  }
  process.exit(report.status === 'ok' ? 0 : report.status === 'violations' ? 1 : report.status === 'incomplete' ? 4 : 2);
} else if (cmd === 'verify') {
  const file = args[1];
  if (!file) fail('missing evidence file');
  const payload = readJson(file);
  if (!payload || !Array.isArray(payload.events) || !payload.claim) {
    fail('evidence file must be {"claim": {...}, "events": [...]}');
  }
  const res = verifyEvidence(payload.events, payload.claim);
  console.log(JSON.stringify(res, null, 2));
  process.exit(res.ok ? 0 : 1);
} else if (cmd === 'samples') {
  const dir = args[1] || 'samples';
  mkdirSync(dir, { recursive: true });
  for (const [name, build] of Object.entries(samples)) {
    writeFileSync(join(dir, name + '.json'), JSON.stringify(build(), null, 2) + '\n');
    console.log('wrote ' + join(dir, name + '.json'));
  }
} else {
  console.error(USAGE);
  process.exit(cmd === undefined || cmd === '--help' || cmd === '-h' ? 0 : 2);
}
