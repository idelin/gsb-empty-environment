import * as fs from 'fs';
import * as path from 'path';
import { demoScenario } from './demo';
import { formatPlan } from './report';
import { planMigration } from './solver';
import { Scenario } from './types';

function loadScenario(argv: string[]): Scenario {
  const fileArg = argv.find((arg) => !arg.startsWith('--'));
  if (!fileArg) return demoScenario;
  const filePath = path.resolve(process.cwd(), fileArg);
  const raw = fs.readFileSync(filePath, 'utf8');
  return JSON.parse(raw) as Scenario;
}

function main(): void {
  const argv = process.argv.slice(2);
  const scenario = loadScenario(argv);
  const plan = planMigration(scenario);
  if (argv.includes('--json')) {
    process.stdout.write(JSON.stringify(plan, null, 2) + '\n');
  } else {
    process.stdout.write(formatPlan(plan) + '\n');
  }
  if (!plan.feasible) process.exitCode = 1;
}

main();
