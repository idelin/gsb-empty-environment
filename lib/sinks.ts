import {
  TrustedHTML,
  TrustedScript,
  TrustedScriptURL,
} from "./types.ts";
import {
  DEFAULT_POLICY_NAME,
  trustedTypeName,
  type TrustedTypePolicy,
  type TrustedTypePolicyFactory,
} from "./policy.ts";

const TAG = /<[^>]*>/g;
/** Matches an event-handler content attribute (e.g. `onclick`, `ONLOAD`) inside a tag. */
const EVENT_HANDLER_ATTRIBUTE =
  /[\s/]+([A-Za-z][A-Za-z0-9_:-]*)(?=[\s=>]|$)/g;

function hasEventHandlerAttribute(markup: string): boolean {
  for (const tag of markup.matchAll(TAG)) {
    if (tag[0][1] === "/") {
      continue;
    }
    for (const attribute of tag[0].matchAll(EVENT_HANDLER_ATTRIBUTE)) {
      if (/^on[A-Za-z]/i.test(attribute[1])) {
        return true;
      }
    }
  }
  return false;
}

/** A DOM sink that only accepts a particular trusted brand. */
export interface SinkEnforcer {
  /** innerHTML sink: TrustedHTML without event-handler attributes, or a default-policy string. */
  setHTML(value: TrustedHTML | string): string;
  /** Script-body sink: TrustedScript, or a string resolved via the default policy. */
  setScript(value: TrustedScript | string): string;
  /** Script-address sink: TrustedScriptURL (never javascript:/data:), or a default-policy string. */
  setScriptURL(value: TrustedScriptURL | string): string;
}

function rejectMismatch(expected: string, actual: unknown): never {
  const actualName =
    actual === null || actual === undefined
      ? String(actual)
      : typeof actual === "object"
        ? trustedTypeName(actual as never)
        : typeof actual;
  throw new TypeError(
    `Sink requires ${expected}, received ${actualName}; trusted results cannot be swapped between sinks.`,
  );
}

export class TrustedTypeEnforcer implements SinkEnforcer {
  readonly #factory: TrustedTypePolicyFactory;

  constructor(factory: TrustedTypePolicyFactory) {
    this.#factory = factory;
  }

  setHTML(value: TrustedHTML | string): string {
    const html = this.#resolve(value, TrustedHTML, "TrustedHTML", "createHTML");
    if (hasEventHandlerAttribute(html.toString())) {
      throw new TypeError(
        "Refused to assign innerHTML: markup contains an event-handler attribute.",
      );
    }
    return html.toString();
  }

  setScript(value: TrustedScript | string): string {
    const script = this.#resolve(value, TrustedScript, "TrustedScript", "createScript");
    return script.toString();
  }

  setScriptURL(value: TrustedScriptURL | string): string {
    const url = this.#resolve(
      value,
      TrustedScriptURL,
      "TrustedScriptURL",
      "createScriptURL",
    );
    return url.toString();
  }

  #resolve<T extends object>(
    value: T | string,
    brand: new (value: string) => T,
    expectedName: string,
    method: "createHTML" | "createScript" | "createScriptURL",
  ): T {
    if (typeof value === "string") {
      const policy = this.#defaultPolicyOrThrow(expectedName);
      const result = policy[method](value);
      if (!(result instanceof brand)) {
        rejectMismatch(expectedName, result);
      }
      return result;
    }

    if (!(value instanceof brand)) {
      rejectMismatch(expectedName, value);
    }
    return value;
  }

  #defaultPolicyOrThrow(expectedName: string): TrustedTypePolicy {
    const policy = this.#factory.defaultPolicy;
    if (policy === null) {
      throw new TypeError(
        `Sink requires ${expectedName}; no "${DEFAULT_POLICY_NAME}" policy is available to upgrade the string.`,
      );
    }
    return policy;
  }
}
