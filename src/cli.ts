import { readFileSync } from 'node:fs';
import type { EventLog, LogEvent } from './model.ts';
import { varKey } from './model.ts';
import { verifySlice } from './verify.ts';

function describeEvent(e: LogEvent): string {
  const ctrl = e.control.length > 0 ? ` control=[${e.control.join(',')}]` : '';
  const reads = e.reads.length > 0 ? ` reads=[${e.reads.map(varKey).join(',')}]` : '';
  switch (e.kind) {
    case 'write':
      return `#${e.id} write ${varKey(e.target)} = ${JSON.stringify(e.value)}${reads}${ctrl}`;
    case 'cond':
      return `#${e.id} cond -> ${JSON.stringify(e.value)}${reads}${ctrl}`;
    case 'assert':
      return `#${e.id} assert "${e.label}" expected=${JSON.stringify(e.expected)} actual=${JSON.stringify(e.value)} passed=${e.passed}${reads}${ctrl}`;
  }
}

function main(argv: string[]): number {
  const args = argv.slice(2);
  if (args.length < 1) {
    console.error('usage: node src/cli.ts <log.json> [--assert <eventId>]');
    return 2;
  }
  const logPath = args[0];
  let assertId: number | undefined;
  const flagIdx = args.indexOf('--assert');
  if (flagIdx >= 0) assertId = Number(args[flagIdx + 1]);

  const log = JSON.parse(readFileSync(logPath, 'utf8')) as EventLog;

  if (assertId === undefined) {
    const failed = log.events.filter((e) => e.kind === 'assert' && !e.passed);
    if (failed.length === 0) {
      console.error('no failed assertion in log; pass --assert <eventId>');
      return 2;
    }
    assertId = failed[failed.length - 1].id;
  }

  const report = verifySlice(log, assertId);

  console.log(`== slice for assertion #${assertId} ==`);
  for (const e of report.slice.events) console.log('  ' + describeEvent(e));

  console.log(`== gaps (${report.slice.gaps.length}) ==`);
  for (const gap of report.slice.gaps) {
    if (gap.type === 'missing-producer') {
      console.log(`  MISSING PRODUCER: ${varKey(gap.ref)} needed by event #${gap.neededBy}`);
    } else {
      console.log(`  MISSING CONTROL EVENT: #${gap.eventId} needed by event #${gap.neededBy}`);
    }
  }

  console.log('== replay ==');
  for (const gap of report.replay.gaps) {
    console.log(`  event #${gap.eventId}: cannot evaluate, missing ${varKey(gap.missing)}`);
  }
  for (const m of report.replay.mismatches) {
    console.log(`  event #${m.eventId}: recorded=${JSON.stringify(m.recorded)} replayed=${JSON.stringify(m.replayed)}`);
  }
  if (report.replay.assertion) {
    const a = report.replay.assertion;
    console.log(`  assertion #${a.eventId}: recorded=${JSON.stringify(a.recorded)} replayed=${JSON.stringify(a.replayed)} match=${a.match}`);
  } else {
    console.log('  assertion could not be replayed');
  }

  console.log(`== verdict: ${report.ok ? 'OK (slice reproduces the assertion value)' : report.unverifiable ? 'UNVERIFIABLE (gaps in log)' : 'MISMATCH'} ==`);
  return report.ok ? 0 : 1;
}

process.exitCode = main(process.argv);
