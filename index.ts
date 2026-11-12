export type {
  ContributionGate,
  ContributionOptions,
  GateConfig,
  HistogramContribution,
  HostCapabilities,
  HostGateHandle,
  PreparedReport,
  ReportRecord,
  ReportSink,
  StoredContribution,
} from './src/types.ts';

export { createContributionGate } from './src/gate.ts';
export {
  MAX_BUCKET,
  MAX_FILTERING_ID,
  MIN_FILTERING_ID,
  buildOriginWhitelist,
  normalizeOrigin,
} from './src/validation.ts';
