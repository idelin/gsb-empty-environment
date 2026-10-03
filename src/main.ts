import { standardScenario } from './faultInjection.ts';
import { MetricsAnalyzer } from './metrics.ts';
import { renderReport } from './report.ts';
import { SwimCluster, SwimDetector, simulate } from './swim.ts';

export function runStandardScenario(): string {
  const scenario = standardScenario();
  const cluster = new SwimCluster(scenario.clusterSize);
  for (const event of scenario.events) {
    cluster.applyFault(event);
  }
  const detector = new SwimDetector(cluster);
  const detectorLog = simulate(cluster, detector, scenario.totalCycles);
  const result = new MetricsAnalyzer(scenario.events, detectorLog).analyze();
  return renderReport(scenario, result);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  console.log(runStandardScenario());
}
