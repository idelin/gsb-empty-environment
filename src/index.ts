export { PolicyEngine } from './engine.ts';
export { matchesPrefix, normalizePath, segments, depth } from './normalize.ts';
export { decideFlat, expandAll, shadowedFlat } from './model.ts';
export type {
  Decision,
  Effect,
  GroupConfig,
  GroupExplanation,
  PolicyConfig,
  Rule,
  RuleRef,
  ShadowedEntry,
  UpdateError,
  UpdateResult,
} from './types.ts';
