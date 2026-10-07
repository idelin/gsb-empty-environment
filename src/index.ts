export {
  parsePattern,
  expand,
  PatternError,
  type Pattern,
  type Segment,
  type Element,
  type Variant,
} from "./pattern.ts";
export {
  comparePriority,
  decodePath,
  matchVariant,
  type Params,
  type Priority,
} from "./match.ts";
export {
  bestPriority,
  encodePath,
  enumerateConflicts,
  findConflicts,
  intersect,
  type CompiledRoute,
  type Conflict,
} from "./conflict.ts";
export {
  ConflictError,
  Router,
  type Match,
  type Route,
  type RouteDef,
} from "./router.ts";
