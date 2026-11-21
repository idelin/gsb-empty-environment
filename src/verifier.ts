import {
  negotiateSubprotocol,
  parseExtensions,
  parseSubprotocols,
} from './extensions.ts';
import type { ExtensionOffer } from './extensions.ts';
import { HandshakeErrorCode, HandshakeRejected } from './errors.ts';
import { originMatches, parseOrigin } from './origin.ts';
import { extractForwardedInfo } from './proxy.ts';
import {
  parseTicket,
  ticketMatchesRequest,
} from './ticket.ts';
import type { HandshakeTicket, TicketStore } from './ticket.ts';

export interface HandshakeHeaders {
  [name: string]: string | undefined;
}

export interface HandshakeRequest {
  scope: string;
  headers: HandshakeHeaders;
}

export interface HandshakeVerifierConfig {
  siteOrigin: string;
  trustedOrigins: string[];
  acceptedSubprotocols: string[];
  ticketStore: TicketStore;
  trustedProxy?: boolean;
  now?: () => number;
}

export interface AcceptedHandshake {
  origin: string;
  crossSite: boolean;
  subprotocol: string | null;
  extensions: ExtensionOffer[];
  ticket: HandshakeTicket;
  viaTrustedProxy: boolean;
}

const COMPRESSION_EXTENSION = 'permessage-deflate';
const PROXY_HEADERS = ['forwarded', 'x-forwarded-host', 'x-forwarded-proto'];

function hasProxyHeaders(headers: HandshakeHeaders): boolean {
  return PROXY_HEADERS.some((name) => {
    const value = headers[name];
    return typeof value === 'string' && value.trim().length > 0;
  });
}

function forwardedHostIsSite(
  forwardedHost: string | undefined,
  siteOrigin: string,
): boolean {
  if (!forwardedHost) {
    return false;
  }
  const site = parseOrigin(siteOrigin);
  const normalized = forwardedHost.split(',')[0]?.trim().toLowerCase() ?? '';
  const [hostPart, portPart] = normalized.split(':');
  if (hostPart !== site.hostname) {
    return false;
  }
  if (portPart === undefined) {
    return true;
  }
  const siteDefaultPort =
    site.scheme === 'https' || site.scheme === 'wss' ? 443 : 80;
  return Number(portPart) === (site.port ?? siteDefaultPort);
}

export class HandshakeOriginVerifier {
  private readonly config: HandshakeVerifierConfig & {
    trustedProxy: boolean;
    now: () => number;
  };

  constructor(config: HandshakeVerifierConfig) {
    parseOrigin(config.siteOrigin);
    for (const origin of config.trustedOrigins) {
      parseOrigin(origin);
    }
    this.config = {
      ...config,
      trustedProxy: config.trustedProxy ?? false,
      now: config.now ?? (() => Date.now()),
    };
  }

  verify(request: HandshakeRequest): AcceptedHandshake {
    const { headers, scope } = request;
    const now = this.config.now();

    const rawTicket = headers['sec-websocket-ticket'];
    if (typeof rawTicket !== 'string' || rawTicket.trim().length === 0) {
      throw new HandshakeRejected(
        HandshakeErrorCode.MISSING_TICKET,
        'handshake must present a one-time ticket',
      );
    }
    const ticketPayload = parseTicket(rawTicket);

    const offeredSubprotocols = parseSubprotocols(
      headers['sec-websocket-protocol'],
    );
    const offeredExtensions = parseExtensions(
      headers['sec-websocket-extensions'],
    );

    const viaTrustedProxy = this.verifyProxyHeaders(headers);
    const { origin, crossSite } = this.evaluateOrigin(headers);

    if (
      crossSite &&
      offeredExtensions.some(
        (extension) => extension.name === COMPRESSION_EXTENSION,
      )
    ) {
      throw new HandshakeRejected(
        HandshakeErrorCode.CROSS_SITE_COMPRESSION_REJECTED,
        'permessage-deflate cannot be negotiated on a cross-site handshake',
      );
    }

    if (crossSite) {
      this.rejectCrossSiteUpgrade(origin);
    }

    const subprotocol = negotiateSubprotocol(
      offeredSubprotocols,
      this.config.acceptedSubprotocols,
    );
    if (offeredSubprotocols.length > 0 && subprotocol === null) {
      throw new HandshakeRejected(
        HandshakeErrorCode.UNACCEPTABLE_SUBPROTOCOL,
        'none of the requested subprotocols are accepted',
      );
    }

    const scopeError = ticketMatchesRequest(ticketPayload, origin, scope);
    if (scopeError) {
      throw new HandshakeRejected(
        scopeError,
        'ticket does not match the requested origin or scope',
      );
    }
    if (ticketPayload.expiresAt <= now) {
      throw new HandshakeRejected(
        HandshakeErrorCode.TICKET_EXPIRED,
        'handshake ticket has expired',
      );
    }

    let ticket: HandshakeTicket;
    try {
      const consumed = this.config.ticketStore.consume(ticketPayload.nonce, now);
      if (!consumed) {
        throw new HandshakeRejected(
          HandshakeErrorCode.TICKET_ALREADY_USED,
          'one-time ticket has already been consumed',
        );
      }
      ticket = consumed;
    } catch (error) {
      if (error instanceof HandshakeRejected) {
        throw error;
      }
      throw new HandshakeRejected(
        HandshakeErrorCode.MALFORMED_TICKET,
        'ticket store rejected the ticket',
      );
    }

    return {
      origin,
      crossSite,
      subprotocol,
      extensions: offeredExtensions,
      ticket,
      viaTrustedProxy,
    };
  }

  private verifyProxyHeaders(
    headers: HandshakeHeaders,
  ): boolean {
    if (!hasProxyHeaders(headers)) {
      return false;
    }
    if (!this.config.trustedProxy) {
      throw new HandshakeRejected(
        HandshakeErrorCode.UNTRUSTED_PROXY_HEADERS,
        'proxy forwarding headers are only accepted from trusted proxies',
      );
    }
    const info = extractForwardedInfo(headers);
    if (!info || !forwardedHostIsSite(info.host, this.config.siteOrigin)) {
      throw new HandshakeRejected(
        HandshakeErrorCode.PROXY_CROSS_SITE_FORWARD,
        'proxy forwarded a cross-site handshake',
      );
    }
    return true;
  }

  private evaluateOrigin(headers: HandshakeHeaders): {
    origin: string;
    crossSite: boolean;
  } {
    const rawOrigin = headers['origin'];
    if (typeof rawOrigin !== 'string' || rawOrigin.trim().length === 0) {
      throw new HandshakeRejected(
        HandshakeErrorCode.MISSING_ORIGIN,
        'handshake must carry an Origin header',
      );
    }
    try {
      parseOrigin(rawOrigin);
    } catch {
      throw new HandshakeRejected(
        HandshakeErrorCode.MALFORMED_ORIGIN,
        `malformed Origin header: ${rawOrigin}`,
      );
    }

    const crossSite = !originMatches(rawOrigin, this.config.siteOrigin);
    return { origin: rawOrigin, crossSite };
  }

  private rejectCrossSiteUpgrade(origin: string): never {
    const isTrusted = this.config.trustedOrigins.some((trusted) =>
      originMatches(origin, trusted),
    );
    if (!isTrusted) {
      throw new HandshakeRejected(
        HandshakeErrorCode.UNTRUSTED_ORIGIN,
        `origin is not in the trusted site list: ${origin}`,
      );
    }
    throw new HandshakeRejected(
      HandshakeErrorCode.CROSS_SITE_UPGRADE_REJECTED,
      'cross-site WebSocket upgrades are not permitted',
    );
  }
}
