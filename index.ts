export {
  type Origin,
  createOrigin,
  parseOrigin,
  originToString,
  isSameOrigin,
} from "./src/origin.ts";

export {
  requestsOriginKeyedAgentCluster,
  isOriginKeyed,
} from "./src/agentCluster.ts";

export {
  SecurityError,
  DocumentModel,
  type DocumentOptions,
} from "./src/document.ts";

export {
  runDomainLockTrial,
  adjudicateScenario,
  type PipelineConfig,
  type ScenarioInput,
  type ScenarioVerdict,
  type AssignmentResult,
  type DomainLockReport,
} from "./src/pipeline.ts";
