export {
  erf,
  normalCdf,
  midRanks,
  mannWhitneyU,
  quantile,
  type MannWhitneyResult,
} from "./stats.js";
export {
  judgeMetric,
  type Direction,
  type NoDataPolicy,
  type MetricVerdict,
  type MetricConfig,
  type MetricResult,
} from "./judge.js";
export {
  aggregateMetrics,
  type MetricGroup,
  type AggregateConfig,
  type AggregateVerdict,
  type AggregateResult,
  type MetricSamples,
} from "./aggregate.js";
export {
  CanaryStateMachine,
  type StateMachineConfig,
  type MachineStatus,
  type MachineState,
} from "./stateMachine.js";
