import { runScenario, standardScenario } from './scenario.ts';
import { generateReport } from './report.ts';

const seedArg = process.argv[2];
const seed = seedArg === undefined ? undefined : Number.parseInt(seedArg, 10);
if (seed !== undefined && Number.isNaN(seed)) {
  console.error(`无效种子: ${seedArg}`);
  process.exit(1);
}

const { metrics } = runScenario(standardScenario(seed));
console.log(generateReport(metrics, standardScenario(seed).name));
