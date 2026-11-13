export const trustedTypeBrand: unique symbol = Symbol("trusted-type");

/**
 * Marker shared by every trusted result so a plain `string` can never be
 * mistaken for a trusted value. The concrete classes below additionally give
 * each kind its own nominal identity, making HTML / Script / ScriptURL
 * non-interchangeable.
 */
export interface TrustedType {
  readonly [trustedTypeBrand]: true;
  toString(): string;
}

/** Branded result of `createHTML`, accepted by HTML sinks such as innerHTML. */
export class TrustedHTML implements TrustedType {
  readonly [trustedTypeBrand] = true as const;

  readonly #value: string;

  constructor(value: string) {
    this.#value = value;
  }

  toString(): string {
    return this.#value;
  }
}

/** Branded result of `createScript`, accepted by script-body sinks. */
export class TrustedScript implements TrustedType {
  readonly [trustedTypeBrand] = true as const;

  readonly #value: string;

  constructor(value: string) {
    this.#value = value;
  }

  toString(): string {
    return this.#value;
  }
}

/** Branded result of `createScriptURL`, accepted only by script-address sinks. */
export class TrustedScriptURL implements TrustedType {
  readonly [trustedTypeBrand] = true as const;

  readonly #value: string;

  constructor(value: string) {
    this.#value = value;
  }

  toString(): string {
    return this.#value;
  }
}

export type AnyTrustedType = TrustedHTML | TrustedScript | TrustedScriptURL;

export function isHTML(value: unknown): value is TrustedHTML {
  return value instanceof TrustedHTML;
}

export function isScript(value: unknown): value is TrustedScript {
  return value instanceof TrustedScript;
}

export function isScriptURL(value: unknown): value is TrustedScriptURL {
  return value instanceof TrustedScriptURL;
}
