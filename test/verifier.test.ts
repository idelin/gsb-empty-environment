import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  HandshakeErrorCode,
  HandshakeOriginVerifier,
  HandshakeRejected,
  HandshakeTicketIssuer,
  InMemoryTicketStore,
} from '../src/index.ts';

const SITE = 'https://gateway.example.com';
const SCOPE = '/realtime/gateway';

interface TestClock {
  current: number;
  now: () => number;
  advance: (ms: number) => void;
}

function createClock(start = 1_000_000): TestClock {
  let current = start;
  return {
    get current() {
      return current;
    },
    set current(value: number) {
      current = value;
    },
    now() {
      return current;
    },
    advance(ms: number) {
      current += ms;
    },
  };
}

function createHarness(options: { trustedProxy?: boolean } = {}) {
  const clock = createClock();
  const issuer = new HandshakeTicketIssuer({ ttlMs: 60_000 });
  const ticketStore = new InMemoryTicketStore();
  const verifier = new HandshakeOriginVerifier({
    siteOrigin: SITE,
    trustedOrigins: ['https://partner.example.com'],
    acceptedSubprotocols: ['chat.v2', 'events.v1'],
    ticketStore,
    trustedProxy: options.trustedProxy ?? false,
    now: clock.now,
  });

  const issue = (origin = SITE, scope = SCOPE) => {
    const ticket = issuer.issue(origin, scope, clock.now());
    ticketStore.add(ticket);
    return JSON.stringify(ticket);
  };

  return { clock, issuer, ticketStore, verifier, issue };
}

function baseHeaders(ticket: string, origin = SITE): Record<string, string> {
  return {
    origin,
    'sec-websocket-ticket': ticket,
    'sec-websocket-protocol': 'chat.v2',
  };
}

function rejectCode(error: unknown): HandshakeErrorCode {
  assert.ok(error instanceof HandshakeRejected);
  return error.code;
}

describe('same-site handshake', () => {
  it('accepts an upgrade with a valid one-time ticket and negotiates the subprotocol', () => {
    const { verifier, issue } = createHarness();
    const result = verifier.verify({
      scope: SCOPE,
      headers: baseHeaders(issue()),
    });

    assert.equal(result.crossSite, false);
    assert.equal(result.origin, SITE);
    assert.equal(result.subprotocol, 'chat.v2');
    assert.equal(result.viaTrustedProxy, false);
  });

  it('normalizes default ports when comparing origins', () => {
    const { verifier, issue } = createHarness();
    const result = verifier.verify({
      scope: SCOPE,
      headers: baseHeaders(issue(), 'https://gateway.example.com:443'),
    });
    assert.equal(result.origin, 'https://gateway.example.com:443');
  });

  it('rejects an upgrade without an Origin header', () => {
    const { verifier, issue } = createHarness();
    const headers = baseHeaders(issue());
    delete headers.origin;
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers }),
      (error) => rejectCode(error) === HandshakeErrorCode.MISSING_ORIGIN,
    );
  });

  it('rejects a malformed Origin header', () => {
    const { verifier, issue } = createHarness();
    assert.throws(
      () =>
        verifier.verify({
          scope: SCOPE,
          headers: baseHeaders(issue(), 'not-an-origin'),
        }),
      (error) => rejectCode(error) === HandshakeErrorCode.MALFORMED_ORIGIN,
    );
  });
});

describe('cross-site upgrades', () => {
  it('rejects an untrusted cross-site origin', () => {
    const { verifier, issue } = createHarness();
    assert.throws(
      () =>
        verifier.verify({
          scope: SCOPE,
          headers: baseHeaders(issue(), 'https://attacker.example.org'),
        }),
      (error) => rejectCode(error) === HandshakeErrorCode.UNTRUSTED_ORIGIN,
    );
  });

  it('rejects a trusted cross-site origin as an unauthorized upgrade', () => {
    const { verifier, issue } = createHarness();
    assert.throws(
      () =>
        verifier.verify({
          scope: SCOPE,
          headers: baseHeaders(issue(), 'https://partner.example.com'),
        }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.CROSS_SITE_UPGRADE_REJECTED,
    );
  });

  it('rejects permessage-deflate offered on a cross-site handshake', () => {
    const { verifier, issue } = createHarness();
    const headers = baseHeaders(
      issue(),
      'https://partner.example.com',
    );
    headers['sec-websocket-extensions'] =
      'permessage-deflate; client_max_window_bits';
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers }),
      (error) =>
        rejectCode(error) ===
        HandshakeErrorCode.CROSS_SITE_COMPRESSION_REJECTED,
    );
  });

  it('accepts permessage-deflate on a same-site handshake', () => {
    const { verifier, issue } = createHarness();
    const headers = baseHeaders(issue());
    headers['sec-websocket-extensions'] =
      'permessage-deflate; client_max_window_bits';
    const result = verifier.verify({ scope: SCOPE, headers });
    assert.equal(result.extensions[0]?.name, 'permessage-deflate');
  });
});

describe('one-time ticket enforcement', () => {
  it('requires a ticket header', () => {
    const { verifier } = createHarness();
    const headers = baseHeaders('');
    delete headers['sec-websocket-ticket'];
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers }),
      (error) => rejectCode(error) === HandshakeErrorCode.MISSING_TICKET,
    );
  });

  it('rejects malformed ticket payloads', () => {
    const { verifier } = createHarness();
    assert.throws(
      () =>
        verifier.verify({
          scope: SCOPE,
          headers: baseHeaders('not-json'),
        }),
      (error) => rejectCode(error) === HandshakeErrorCode.MALFORMED_TICKET,
    );
  });

  it('rejects ticket replay on the second handshake', () => {
    const { verifier, issue, ticketStore } = createHarness();
    const ticket = issue();

    const first = verifier.verify({ scope: SCOPE, headers: baseHeaders(ticket) });
    assert.equal(first.ticket.nonce.length > 0, true);
    assert.equal(ticketStore.size, 0);

    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers: baseHeaders(ticket) }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.TICKET_ALREADY_USED,
    );
  });

  it('rejects an expired ticket', () => {
    const { verifier, issue, clock } = createHarness();
    const ticket = issue();
    clock.advance(61_000);
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers: baseHeaders(ticket) }),
      (error) => rejectCode(error) === HandshakeErrorCode.TICKET_EXPIRED,
    );
  });

  it('rejects a ticket scoped to a different path', () => {
    const { verifier, issue } = createHarness();
    const ticket = issue(SITE, '/another/service');
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers: baseHeaders(ticket) }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.TICKET_SCOPE_MISMATCH,
    );
  });

  it('rejects a ticket minted for another origin even when replayed same-site', () => {
    const { verifier, issue } = createHarness();
    const ticket = issue('https://partner.example.com');
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers: baseHeaders(ticket) }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.TICKET_ORIGIN_MISMATCH,
    );
  });
});

describe('subprotocol negotiation', () => {
  it('selects the first mutually supported subprotocol', () => {
    const { verifier, issue } = createHarness();
    const headers = baseHeaders(issue());
    headers['sec-websocket-protocol'] = 'unknown.v9, events.v1, chat.v2';
    const result = verifier.verify({ scope: SCOPE, headers });
    assert.equal(result.subprotocol, 'events.v1');
  });

  it('rejects handshakes whose subprotocols are all unsupported', () => {
    const { verifier, issue } = createHarness();
    const headers = baseHeaders(issue());
    headers['sec-websocket-protocol'] = 'legacy.v0';
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.UNACCEPTABLE_SUBPROTOCOL,
    );
  });

  it('allows handshakes without a subprotocol header', () => {
    const { verifier, issue } = createHarness();
    const headers = baseHeaders(issue());
    delete headers['sec-websocket-protocol'];
    const result = verifier.verify({ scope: SCOPE, headers });
    assert.equal(result.subprotocol, null);
  });
});

describe('proxy forwarding', () => {
  it('rejects forwarding headers when the peer is not a trusted proxy', () => {
    const { verifier, issue } = createHarness();
    const headers = baseHeaders(issue());
    headers['x-forwarded-host'] = 'gateway.example.com';
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.UNTRUSTED_PROXY_HEADERS,
    );
  });

  it('accepts same-site forwarding from a trusted proxy', () => {
    const { verifier, issue } = createHarness({ trustedProxy: true });
    const headers = baseHeaders(issue());
    headers['forwarded'] =
      'for=203.0.113.7;proto=https;host=gateway.example.com';
    const result = verifier.verify({ scope: SCOPE, headers });
    assert.equal(result.viaTrustedProxy, true);
  });

  it('rejects cross-site forwarding carried through a trusted proxy', () => {
    const { verifier, issue } = createHarness({ trustedProxy: true });
    const headers = baseHeaders(issue());
    headers['x-forwarded-host'] = 'attacker.example.org';
    headers['x-forwarded-proto'] = 'https';
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.PROXY_CROSS_SITE_FORWARD,
    );
  });

  it('rejects standard Forwarded headers carrying a foreign host', () => {
    const { verifier, issue } = createHarness({ trustedProxy: true });
    const headers = baseHeaders(issue());
    headers['forwarded'] = 'proto=https;host=partner.example.com';
    assert.throws(
      () => verifier.verify({ scope: SCOPE, headers }),
      (error) =>
        rejectCode(error) === HandshakeErrorCode.PROXY_CROSS_SITE_FORWARD,
    );
  });
});
