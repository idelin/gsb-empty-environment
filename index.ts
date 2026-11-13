export {
  TrustedHTML,
  TrustedScript,
  TrustedScriptURL,
  isHTML,
  isScript,
  isScriptURL,
  type AnyTrustedType,
  type TrustedType,
} from "./lib/types.ts";

export {
  DEFAULT_POLICY_NAME,
  TrustedTypePolicyFactory,
  trustedTypeName,
  type TrustedTypePolicy,
  type TrustedTypePolicyOptions,
} from "./lib/policy.ts";

export {
  TrustedTypeEnforcer,
  type SinkEnforcer,
} from "./lib/sinks.ts";

export {
  DocumentElement,
  DocumentPipeline,
  createPipeline,
  type PipelineResult,
} from "./lib/pipeline.ts";

import { TrustedTypePolicyFactory } from "./lib/policy.ts";

/** Shared factory used by code that wants the document-global registry. */
export const trustedTypes = new TrustedTypePolicyFactory();
