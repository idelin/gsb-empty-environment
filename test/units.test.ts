import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  HandshakeErrorCode,
  HandshakeRejected,
  extractForwardedInfo,
  negotiateSubprotocol,
  originMatches,
  parseExtensions,
  parseForwardedHeader,
  parseSubprotocols,
  parseTicket,
  sameOrigin,
} from '../src/index.ts';

describe('origin comparison', () => {
  it('treats scheme, host and default port as the same origin', () => {
    assert.equal(
      sameOrigin(
        { scheme: 'https', hostname: 'a.example', port: null },
        { scheme: 'https', hostname: 'a.example', port: 443 },
      ),
      true,
    );
    assert.equal(
      originMatches('https://a.example:8443', 'https://a.example'),
      false,
    );
    assert.equal(
      originMatches('http://a.example', 'https://a.example'),
      false,
    );
  });
});

describe('header parsing', () => {
  it('parses subprotocol preference lists', () => {
    assert.deepEqual(parseSubprotocols('a, b ,c'), ['a', 'b', 'c']);
    assert.deepEqual(parseSubprotocols(undefined), []);
    assert.equal(negotiateSubprotocol(['x', 'b'], ['b', 'z']), 'b');
    assert.equal(negotiateSubprotocol(['x'], ['b']), null);
  });

  it('parses extension offers with parameters', () => {
    const offers = parseExtensions(
      'permessage-deflate; client_max_window_bits, unknown',
    );
    assert.deepEqual(offers, [
      { name: 'permessage-deflate', params: ['client_max_window_bits'] },
      { name: 'unknown', params: [] },
    ]);
  });

  it('parses RFC 7239 Forwarded headers', () => {
    const parsed = parseForwardedHeader(
      'for=198.51.100.1;proto=https;host="a.example", for=203.0.113.5',
    );
    assert.equal(parsed[0]?.host, 'a.example');
    assert.equal(parsed[0]?.proto, 'https');
    assert.equal(parsed[1]?.origin, '203.0.113.5');
  });

  it('falls back to X-Forwarded-* headers', () => {
    const info = extractForwardedInfo({
      'x-forwarded-host': 'a.example',
      'x-forwarded-proto': 'https',
    });
    assert.deepEqual(info, { host: 'a.example', proto: 'https' });
  });
});

describe('ticket parsing', () => {
  it('rejects non-JSON and incomplete tickets with stable error codes', () => {
    assert.throws(
      () => parseTicket('nope'),
      (error) =>
        error instanceof HandshakeRejected &&
        error.code === HandshakeErrorCode.MALFORMED_TICKET,
    );
    assert.throws(
      () =>
        parseTicket(
          JSON.stringify({
            nonce: 'n',
            origin: 'bad',
            scope: '/s',
            expiresAt: 1,
          }),
        ),
      (error) =>
        error instanceof HandshakeRejected &&
        error.code === HandshakeErrorCode.MALFORMED_TICKET,
    );
  });
});
