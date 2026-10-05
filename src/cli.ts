import { readFileSync } from 'node:fs';
import { replaySlice } from './replay.js';
import { sliceLog, varKey } from './slicer.js';
import type { EventLog, LogEvent, Value } from './types.js';

const USAGE = 'usage: log-slice <log.json> [--assert <event-id>]';

function fmtValue(v: Value): string {
  return JSON.stringify(v);
}

function fmtEvent(ev: LogEvent): string {
  const writes = ev.writes === null ? '-' : varKey(ev.writes);
  const reads = ev.reads.map(varKey).join(',');
  const ctrl = ev.control.join(',');
  const op = ev.op === null ? '-' : ev.op;
  return '#' + String(ev.id) + ' ' + ev.kind + ' op=' + op + ' writes=' + writes + ' reads=[' + reads + '] control=[' + ctrl + '] value=' + fmtValue(ev.value);
}

function findCriterion(log: EventLog, requested: number | null): number | null {
  if (requested !== null) {
    return requested;
  }
  let lastAssert: number | null = null;
  let lastFailed: number | null = null;
  for (const ev of log.events) {
    if (ev.kind === 'assert') {
      lastAssert = ev.id;
      if (ev.value === false) {
        lastFailed = ev.id;
      }
    }
  }
  if (lastFailed !== null) {
    return lastFailed;
  }
  return lastAssert;
}

export function main(argv: string[]): number {
  const args = argv.slice(2);
  let logPath: string | null = null;
  let requested: number | null = null;
  let i = 0;
  while (i < args.length) {
    const a = args[i];
    if (a === '--assert') {
      if (i + 1 >= args.length) {
        console.error('--assert requires an event id');
        return 1;
      }
      requested = Number(args[i + 1]);
      i += 2;
      continue;
    }
    if (logPath === null) {
      logPath = a;
    } else {
      console.error('unexpected argument: ' + a);
      return 1;
    }
    i += 1;
  }
  if (logPath === null) {
    console.error(USAGE);
    return 1;
  }

  const log: EventLog = JSON.parse(readFileSync(logPath, 'utf8'));
  const criterion = findCriterion(log, requested);
  if (criterion === null) {
    console.error('no assertion event found in log');
    return 1;
  }

  const slice = sliceLog(log, criterion);
  const report = replaySlice(slice);

  console.log('criterion: assertion event #' + String(criterion));
  console.log('slice: ' + String(slice.events.length) + ' event(s)');
  for (const ev of slice.events) {
    console.log('  ' + fmtEvent(ev));
  }
  console.log('provenance edges:');
  for (const e of slice.edges) {
    if (e.kind === 'data' && e.variable !== null) {
      console.log('  #' + String(e.from) + ' --data ' + varKey(e.variable) + '--> #' + String(e.to));
    } else {
      console.log('  #' + String(e.from) + ' --control--> #' + String(e.to));
    }
  }
  if (slice.gaps.length === 0) {
    console.log('gaps: none');
  } else {
    console.log('gaps: ' + String(slice.gaps.length) + ' missing dependency(ies)');
    for (const g of slice.gaps) {
      console.log('  [' + g.kind + '] ' + g.message);
    }
  }
  const actual = report.actual === null ? 'unknown' : fmtValue(report.actual);
  console.log('replay: ' + report.verdict.toUpperCase() + ' (recorded=' + fmtValue(report.expected) + ' replayed=' + actual + ')');
  if (report.unknownEvents.length > 0) {
    console.log('  unreplayed events: ' + report.unknownEvents.join(','));
  }
  if (report.verdict === 'match') {
    return 0;
  }
  if (report.verdict === 'mismatch') {
    return 2;
  }
  return 3;
}

process.exit(main(process.argv));
