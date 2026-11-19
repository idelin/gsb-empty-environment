import { ProtocolError } from './errors.ts';

export const CAP_MESSAGE_TAGS = 'message-tags';

export interface CapNegotiationReply {
  subcommand: 'LS' | 'ACK' | 'NAK';
  /** Capability tokens, without values, e.g. `message-tags`. */
  capabilities: string[];
  /** LS replies may be split across several messages; `done` ends the list. */
  done?: boolean;
}

/**
 * Registry of the capabilities the server supports. Unknown capabilities are
 * simply absent, which makes REQ validation an all-or-nothing check.
 */
export class CapabilityRegistry {
  private readonly supported: ReadonlySet<string>;

  constructor(supported: readonly string[] = [CAP_MESSAGE_TAGS]) {
    this.supported = new Set(supported);
  }

  isSupported(capability: string): boolean {
    return this.supported.has(capability);
  }

  list(): string[] {
    return [...this.supported];
  }
}

/**
 * Per-connection CAP state: which LS version was negotiated, the capabilities
 * the client has ACKed, and whether registration is suspended.
 */
export class ConnectionCapState {
  /** Highest CAP version requested by the client (301/302), or 301 default. */
  lsVersion = 301;
  private readonly acknowledged = new Set<string>();
  registrationSuspended = false;

  ack(capabilities: readonly string[]): void {
    for (const capability of capabilities) {
      this.acknowledged.add(capability);
    }
  }

  nak(capabilities: readonly string[]): void {
    for (const capability of capabilities) {
      this.acknowledged.delete(capability);
    }
  }

  has(capability: string): boolean {
    return this.acknowledged.has(capability);
  }

  acknowledgedCapabilities(): string[] {
    return [...this.acknowledged];
  }
}

export class CapNegotiator {
  private readonly registry: CapabilityRegistry;
  private readonly state: ConnectionCapState;

  constructor(
    registry: CapabilityRegistry = new CapabilityRegistry(),
    state: ConnectionCapState = new ConnectionCapState(),
  ) {
    this.registry = registry;
    this.state = state;
  }

  getState(): ConnectionCapState {
    return this.state;
  }

  /**
   * Handle a CAP command. Throws {@link ProtocolError} for malformed
   * subcommands; CAP REQ is atomic and either fully ACKs or fully NAKs.
   */
  handle(params: string[]): CapNegotiationReply | undefined {
    if (params.length === 0) {
      throw new ProtocolError('CAP requires a subcommand');
    }
    const subcommand = params[0].toUpperCase();

    switch (subcommand) {
      case 'LS': {
        const version = params[1] !== undefined ? Number(params[1]) : 301;
        if (!Number.isInteger(version) || (version !== 301 && version !== 302)) {
          throw new ProtocolError(`unsupported CAP LS version: ${params[1]}`);
        }
        this.state.lsVersion = version;
        if (version === 302) {
          // CAP LS 302 suspends registration until CAP END (or CAP CANCEL).
          this.state.registrationSuspended = true;
        }
        return {
          subcommand: 'LS',
          capabilities: this.registry.list(),
          done: true,
        };
      }
      case 'REQ': {
        if (params.length < 2) {
          throw new ProtocolError('CAP REQ requires a capability list');
        }
        const requested = params[1]
          .split(' ')
          .map((token) => token.trim())
          .filter((token) => token.length > 0)
          .map((token) => (token.startsWith('-') ? token.slice(1) : token));
        if (requested.length === 0) {
          throw new ProtocolError('CAP REQ requires a capability list');
        }
        const allSupported = requested.every((capability) =>
          this.registry.isSupported(capability),
        );
        if (!allSupported) {
          // Whole list is refused; no partial capability is enabled.
          this.state.nak(requested);
          return { subcommand: 'NAK', capabilities: requested };
        }
        this.state.ack(requested);
        return { subcommand: 'ACK', capabilities: requested };
      }
      case 'END':
        this.state.registrationSuspended = false;
        return undefined;
      case 'CANCEL': {
        if (params.length < 2) {
          throw new ProtocolError('CAP CANCEL requires a capability list');
        }
        const capabilities = params[1].split(' ').filter((token) => token.length > 0);
        this.state.nak(capabilities);
        this.state.registrationSuspended = false;
        return { subcommand: 'NAK', capabilities };
      }
      case 'LIST':
        return {
          subcommand: 'LS',
          capabilities: this.state.acknowledgedCapabilities(),
          done: true,
        };
      default:
        throw new ProtocolError(`unknown CAP subcommand: ${subcommand}`);
    }
  }
}
