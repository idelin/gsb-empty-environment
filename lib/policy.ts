import {
  TrustedHTML,
  TrustedScript,
  TrustedScriptURL,
  isHTML,
  isScript,
  isScriptURL,
  type AnyTrustedType,
  type TrustedType,
} from "./types.ts";

export const DEFAULT_POLICY_NAME = "default";

/** Schemes that may never back a script address, regardless of any policy. */
const FORBIDDEN_SCRIPT_URL_SCHEMES = new Set(["javascript:", "data:"]);

export interface TrustedTypePolicyOptions {
  createHTML?: (input: string) => string;
  createScript?: (input: string) => string;
  createScriptURL?: (input: string) => string;
}

export interface TrustedTypePolicy {
  readonly name: string;
  createHTML(input: string): TrustedHTML;
  createScript(input: string): TrustedScript;
  createScriptURL(input: string): TrustedScriptURL;
}

function assertScriptURLScheme(url: string): void {
  const scheme = /^[A-Za-z][A-Za-z0-9+.-]*:/.exec(url.trim())?.[0].toLowerCase();
  if (scheme !== undefined && FORBIDDEN_SCRIPT_URL_SCHEMES.has(scheme)) {
    throw new TypeError(
      `Refused to create TrustedScriptURL: the "${scheme}" scheme is not allowed.`,
    );
  }
}

class TrustedTypePolicyImpl implements TrustedTypePolicy {
  readonly name: string;

  readonly #options: TrustedTypePolicyOptions;

  constructor(name: string, options: TrustedTypePolicyOptions) {
    this.name = name;
    this.#options = options;
  }

  createHTML(input: string): TrustedHTML {
    const value = this.#options.createHTML?.(input) ?? input;
    return new TrustedHTML(value);
  }

  createScript(input: string): TrustedScript {
    const value = this.#options.createScript?.(input) ?? input;
    return new TrustedScript(value);
  }

  createScriptURL(input: string): TrustedScriptURL {
    const value = this.#options.createScriptURL?.(input) ?? input;
    assertScriptURLScheme(value);
    return new TrustedScriptURL(value);
  }
}

export class TrustedTypePolicyFactory {
  readonly #policies = new Map<string, TrustedTypePolicy>();
  #defaultPolicy: TrustedTypePolicy | null = null;
  #emptyHTML: TrustedHTML | null = null;
  #emptyScript: TrustedScript | null = null;

  get defaultPolicy(): TrustedTypePolicy | null {
    // The default policy only participates while no named policy exists.
    if (this.#policies.size > 0) {
      return null;
    }
    return this.#defaultPolicy;
  }

  get emptyHTML(): TrustedHTML {
    if (this.#emptyHTML === null) {
      this.#emptyHTML = new TrustedHTML("");
    }
    return this.#emptyHTML;
  }

  get emptyScript(): TrustedScript {
    if (this.#emptyScript === null) {
      this.#emptyScript = new TrustedScript("");
    }
    return this.#emptyScript;
  }

  createPolicy(
    name: string,
    options: TrustedTypePolicyOptions = {},
  ): TrustedTypePolicy {
    if (name === DEFAULT_POLICY_NAME) {
      if (this.#defaultPolicy !== null) {
        throw new TypeError(`Policy "${name}" already exists.`);
      }
      const policy = new TrustedTypePolicyImpl(name, options);
      this.#defaultPolicy = policy;
      return policy;
    }

    if (this.#policies.has(name)) {
      throw new TypeError(`Policy "${name}" already exists.`);
    }
    const policy = new TrustedTypePolicyImpl(name, options);
    this.#policies.set(name, policy);
    return policy;
  }

  isHTML(value: unknown): value is TrustedHTML {
    return isHTML(value);
  }

  isScript(value: unknown): value is TrustedScript {
    return isScript(value);
  }

  isScriptURL(value: unknown): value is TrustedScriptURL {
    return isScriptURL(value);
  }

  isPolicy(value: unknown): value is TrustedTypePolicy {
    return (
      value instanceof TrustedTypePolicyImpl ||
      (typeof value === "object" &&
        value !== null &&
        typeof (value as TrustedTypePolicy).name === "string" &&
        typeof (value as TrustedTypePolicy).createHTML === "function" &&
        typeof (value as TrustedTypePolicy).createScript === "function" &&
        typeof (value as TrustedTypePolicy).createScriptURL === "function")
    );
  }

  getAttributeType(_tagName: string, _attribute: string): string | null {
    return null;
  }

  getPropertyType(_tagName: string, _property: string): string | null {
    return null;
  }
}

export function trustedTypeName(value: TrustedType): string {
  if (isHTML(value)) {
    return "TrustedHTML";
  }
  if (isScript(value)) {
    return "TrustedScript";
  }
  if (isScriptURL(value)) {
    return "TrustedScriptURL";
  }
  return "TrustedType";
}

export type { AnyTrustedType };
