/**
 * Capabilities this server understands. Everything else is "unknown" and
 * makes an entire CAP REQ fail atomically.
 */
export const SUPPORTED_CAPABILITIES: ReadonlySet<string> = new Set(["message-tags"]);

export type CapSubcommand = "LS" | "REQ" | "ACK" | "NAK" | "LIST" | "END" | "NEW" | "DEL";

export interface CapRequestResult {
  /** True only when every requested capability is supported (whole-list ACK). */
  acknowledged: boolean;
  /** Capabilities as requested, in order, for echoing back in ACK/NAK. */
  capabilities: string[];
}

/**
 * Per-connection CAP negotiation state.

 * The registration-hold rule is version gated: a `CAP LS 302` (the 302
 * negotiation version) suspends the normal NICK/USER registration until the
 * client sends `CAP END`. Older LS versions do not hold registration.
 */
export class CapNegotiation {
  /** Capabilities currently enabled via an acknowledged REQ. */
  readonly enabled = new Set<string>();

  /** True while registration is held open waiting for CAP END. */
  registrationHeld = false;

  /** Highest LS version advertised by the client (e.g. 302, 301, 300). */
  lsVersion = 0;

  /**
   * Apply an LS request. Only LS 302 holds registration; lower versions are
   * answered but leave registration to proceed normally.
   */
  handleLs(version: number): void {
    this.lsVersion = Math.max(this.lsVersion, version);
    if (version >= 302) {
      this.registrationHeld = true;
    }
  }

  /**
   * Validate a REQ atomically: the full list is acknowledged only when every
   * entry is a supported capability, otherwise the entire request is NAKed
   * and no state changes.
   */
  handleReq(capabilities: string[]): CapRequestResult {
    const allSupported =
      capabilities.length > 0 &&
      capabilities.every((cap) => SUPPORTED_CAPABILITIES.has(cap));

    if (!allSupported) {
      return { acknowledged: false, capabilities };
    }

    for (const cap of capabilities) {
      this.enabled.add(cap);
    }
    return { acknowledged: true, capabilities };
  }

  /** CAP END releases the registration hold. */
  handleEnd(): void {
    this.registrationHeld = false;
  }

  has(capability: string): boolean {
    return this.enabled.has(capability);
  }

  get messageTags(): boolean {
    return this.enabled.has("message-tags");
  }
}

/** Split a REQ/ACK/NAK parameter into individual capability tokens. */
export function splitCapabilityList(list: string): string[] {
  return list.split(" ").filter((token) => token.length > 0);
}
