export {
  CAPABILITIES,
  ALL_CAPABILITY_MASK,
  CapabilitySet,
  toMask,
  maskToCapabilities,
  hasCapability,
  requireCapability,
  intersectMasks,
  removeMask,
  capabilityEquals,
  capabilityMaskNames,
  type Capability,
  type CapabilityInput,
  type CapabilityMask,
} from "./capabilities.ts";

export {
  DescriptionGraph,
  type CallSpec,
  type DataValue,
  type GraphSpec,
  type NodeKind,
  type NodeSpec,
  type Primitive,
  type PropertySpec,
  type PrototypeSpec,
  type RefAlias,
  type RefValue,
} from "./graph.ts";

export {
  PolicyEngine,
  type PolicyConfig,
  type PolicyEdgeDef,
  type PolicyEdgeKind,
  type PolicyEdgeSpec,
  type PolicyNodeDef,
  type PolicyNodeSpec,
  type PolicySpec,
  type RevocationAction,
  type RevocationSpec,
} from "./policy.ts";

export {
  MultiDomainViewHost,
  MultiDomainViewHost as BisimulationHost,
  MultiDomainViewHost as BisimulationViewHost,
  MultiDomainViewHost as MultiDomainHost,
  createHost,
  createBisimulationHost,
  createBisimulationViewHost,
  createMultiDomainBisimulationHost,
  createViewHost,
  createMultiDomainViewHost,
  type HostOptions,
  type HostSnapshot,
  type RequestOptions,
  type RevokeOptions,
  type RevocationEvent,
  type RewrapOptions,
  type ViewOptions,
  type ViewSnapshot,
} from "./host.ts";

export { FrozenMap } from "./immutable.ts";

export {
  BisimulationClass,
  ClassInterner,
  FiniteStateModel,
  bisimilarFiniteStates,
  computeBisimulation,
  type BisimulationResult,
  type FiniteStateSpec,
  type LabelledState,
  type LabelledTransition,
} from "./bisim.ts";

export {
  HostError,
  UnsafeDescriptionError,
  InvalidDescriptionError,
  UnknownNodeError,
  PolicyError,
  CapabilityError,
  RevokedViewError,
  SymbolicOperationError,
  InvalidWriteError,
} from "./errors.ts";

export { createHost as default } from "./host.ts";
