// 确定性回放 CLI：
//   node src/cli.ts replay [--scenario file.json] [--data dir] [--verbose] [--check]
// 相同场景 + 相同代码 => 完全相同的 transcript 与账本字节。
import fs from 'node:fs';
import path from 'node:path';
import { Sim } from './sim.ts';
import type { Scenario } from './sim.ts';
import { auditDir } from './audit.ts';
import type { AuditReport } from './audit.ts';
import { demoScenario } from './scenario.ts';

function parseArgs(argv: string[]): Record<string, string | boolean> {
  const args: Record<string, string | boolean> = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a.startsWith('--')) {
      const key = a.slice(2);
      if (i + 1 < argv.length && !argv[i + 1].startsWith('--')) {
        args[key] = argv[i + 1];
        i++;
      } else {
        args[key] = true;
      }
    }
  }
  return args;
}

function printReport(report: AuditReport): void {
  console.log('=== AUDIT ===');
  for (const w of report.windows) {
    console.log(
      `window ${w.windowId}: budget=${w.budget} consumed=${w.consumed} idle=${w.idle} ${w.ok ? 'OK' : 'VIOLATION'}`,
    );
  }
  console.log(`accepted requests (unique ids): ${report.acceptedUnique}`);
  console.log(`rejections: ${JSON.stringify(report.rejections)}`);
  console.log(`issue-rejections: ${JSON.stringify(report.issueRejections)}`);
  console.log(`conflicts: ${report.conflicts}`);
  if (report.duplicateRequestIds.length > 0) {
    console.log(`duplicate request ids: ${report.duplicateRequestIds.join(', ')}`);
  }
  if (report.unconfirmedReturns.length > 0) {
    console.log(`unconfirmed returns: ${report.unconfirmedReturns.map((r) => `${r.nodeId}/${r.grantId}=${r.unspent}`).join(', ')}`);
  }
  if (report.lostGrants.length > 0) {
    console.log(`grants never received: ${report.lostGrants.map((g) => `${g.grantId}->${g.nodeId}(${g.amount})`).join(', ')}`);
  }
  if (report.violations.length > 0) {
    console.log('violations:');
    for (const v of report.violations) console.log(`  - ${v}`);
  }
  console.log(report.ok ? 'RESULT: OK' : 'RESULT: FAILED');
}

function main(): void {
  const argv = process.argv.slice(2);
  const cmd = argv[0];
  if (cmd !== 'replay') {
    console.error('usage: node src/cli.ts replay [--scenario file.json] [--data dir] [--verbose] [--check]');
    process.exit(2);
  }
  const args = parseArgs(argv.slice(1));
  const scenario: Scenario =
    typeof args.scenario === 'string'
      ? (JSON.parse(fs.readFileSync(args.scenario, 'utf8')) as Scenario)
      : demoScenario;
  const dataDir = typeof args.data === 'string' ? args.data : path.join(process.cwd(), 'out', 'run');

  const sim = new Sim(scenario, dataDir);
  sim.run();
  if (args.verbose) console.log(sim.transcript.join('\n'));

  const report = auditDir(dataDir, scenario.windowLength, scenario.windowBudget);
  printReport(report);

  if (args.check) {
    const sim2 = new Sim(scenario, dataDir + '.replay');
    sim2.run();
    const sameTranscript = sim2.transcript.join('\n') === sim.transcript.join('\n');
    const files1 = fs.readdirSync(dataDir).sort();
    const sameLedgers = files1.every(
      (f) =>
        fs.readFileSync(path.join(dataDir, f), 'utf8') ===
        fs.readFileSync(path.join(dataDir + '.replay', f), 'utf8'),
    );
    console.log(`determinism: transcript ${sameTranscript ? 'IDENTICAL' : 'DIFFERS'}, ledgers ${sameLedgers ? 'IDENTICAL' : 'DIFFER'}`);
    if (!sameTranscript || !sameLedgers) process.exit(1);
  }
  process.exit(report.ok ? 0 : 1);
}

main();
