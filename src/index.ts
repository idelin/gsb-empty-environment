export type {
  Assignment,
  Avoidance,
  AvoidanceWitness,
  CapacityWitness,
  ExhaustedWitness,
  Infeasible,
  Judge,
  SearchStats,
  SolveError,
  SolveOptions,
  SolveResult,
  Solved,
  Witness,
  Work,
} from './types.ts';
export { solve, validateAssignment, computeLoads } from './solver.ts';
export { findAvoidanceWitness, findCapacityWitness } from './witness.ts';
