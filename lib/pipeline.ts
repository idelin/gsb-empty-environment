import { TrustedHTML, TrustedScript, TrustedScriptURL } from "./types.ts";
import { TrustedTypePolicyFactory } from "./policy.ts";
import { TrustedTypeEnforcer, type SinkEnforcer } from "./sinks.ts";

/** Minimal element exposing the sinks the pipeline is allowed to drive. */
export class DocumentElement {
  innerHTML = "";
  scriptText = "";
  scriptSrc = "";
}

export interface PipelineResult {
  readonly innerHTML: string;
  readonly scriptText: string;
  readonly scriptSrc: string;
}

/**
 * Document pipeline. Every write is routed through the trusted-type enforcer
 * so the three branded results are checked against the sink they target.
 */
export class DocumentPipeline {
  readonly #element: DocumentElement;
  readonly #enforcer: SinkEnforcer;

  constructor(
    enforcer: SinkEnforcer,
    element: DocumentElement = new DocumentElement(),
  ) {
    this.#enforcer = enforcer;
    this.#element = element;
  }

  writeInnerHTML(value: TrustedHTML | string): PipelineResult {
    this.#element.innerHTML = this.#enforcer.setHTML(value);
    return this.snapshot();
  }

  writeScript(value: TrustedScript | string): PipelineResult {
    this.#element.scriptText = this.#enforcer.setScript(value);
    return this.snapshot();
  }

  writeScriptURL(value: TrustedScriptURL | string): PipelineResult {
    this.#element.scriptSrc = this.#enforcer.setScriptURL(value);
    return this.snapshot();
  }

  snapshot(): PipelineResult {
    return {
      innerHTML: this.#element.innerHTML,
      scriptText: this.#element.scriptText,
      scriptSrc: this.#element.scriptSrc,
    };
  }
}

export function createPipeline(
  factory: TrustedTypePolicyFactory = new TrustedTypePolicyFactory(),
): {
  factory: TrustedTypePolicyFactory;
  enforcer: TrustedTypeEnforcer;
  pipeline: DocumentPipeline;
} {
  const enforcer = new TrustedTypeEnforcer(factory);
  const pipeline = new DocumentPipeline(enforcer);
  return { factory, enforcer, pipeline };
}
