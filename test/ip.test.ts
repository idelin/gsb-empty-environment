import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  classifyIp,
  InvalidIpError,
  isMorePublic,
  normalizeIp,
  parseIp,
  spaceRank,
} from '../index.ts';

describe('parseIp / normalizeIp', () => {
  it('parses plain IPv4 literals', () => {
    const parsed = parseIp('192.168.1.2');
    assert.equal(parsed.version, 4);
    assert.deepEqual([...parsed.bytes], [192, 168, 1, 2]);
    assert.equal(parsed.embeddedV4, false);
    assert.equal(normalizeIp('192.168.1.2'), '192.168.1.2');
  });

  it('unwraps ::ffff:a.b.c.d mapped literals back to IPv4', () => {
    const mapped = parseIp('::ffff:192.168.1.2');
    assert.equal(mapped.version, 6);
    assert.equal(mapped.embeddedV4, true);
    assert.deepEqual([...mapped.bytes.slice(12)], [192, 168, 1, 2]);

    assert.equal(normalizeIp('::ffff:192.168.1.2'), '192.168.1.2');
    assert.equal(normalizeIp('::FFFF:127.0.0.1'), '127.0.0.1');
    assert.equal(normalizeIp('0:0:0:0:0:ffff:8.8.8.8'), '8.8.8.8');
  });

  it('unwraps legacy IPv4-compatible ::a.b.c.d literals', () => {
    assert.equal(normalizeIp('::192.168.0.1'), '192.168.0.1');
    assert.equal(normalizeIp('::10.0.0.5'), '10.0.0.5');
  });

  it('compresses plain IPv6 literals canonically', () => {
    assert.equal(normalizeIp('2001:0db8:0000:0000:0000:0000:0000:0001'), '2001:db8::1');
    assert.equal(normalizeIp('fe80:0:0:0:0:0:0:1'), 'fe80::1');
    assert.equal(normalizeIp('::1'), '::1');
  });

  it('rejects invalid literals', () => {
    for (const bad of ['', 'not-an-ip', '256.1.1.1', '1.2.3', '01.2.3.4', '::g', '2001:::1', '::ffff:999.0.0.1']) {
      assert.throws(() => parseIp(bad), InvalidIpError, `expected throw for ${bad}`);
    }
  });
});

describe('classifyIp', () => {
  const cases: Array<[string, 'loopback' | 'private' | 'public']> = [
    ['127.0.0.1', 'loopback'],
    ['127.255.255.254', 'loopback'],
    ['::1', 'loopback'],
    ['::ffff:127.0.0.1', 'loopback'],
    ['::ffff:7f00:1', 'loopback'],

    ['10.0.0.1', 'private'],
    ['172.16.0.1', 'private'],
    ['172.31.255.255', 'private'],
    ['192.168.10.10', 'private'],
    ['169.254.1.1', 'private'],
    ['0.0.0.0', 'private'],
    ['::ffff:192.168.0.1', 'private'],
    ['::ffff:10.20.30.40', 'private'],
    ['::192.168.0.1', 'private'],
    ['fe80::1', 'private'],
    ['fc00::1', 'private'],
    ['fd12:3456:789a::1', 'private'],
    ['2001:db8::1', 'private'],
    ['::', 'private'],

    ['8.8.8.8', 'public'],
    ['1.1.1.1', 'public'],
    ['172.32.0.1', 'public'],
    ['::ffff:8.8.8.8', 'public'],
    ['2606:4700:4700::1111', 'public'],
  ];

  for (const [ip, expected] of cases) {
    it(`classifies ${ip} as ${expected}`, () => {
      assert.equal(classifyIp(ip), expected);
    });
  }
});

describe('isMorePublic', () => {
  it('orders spaces loopback < private < public', () => {
    assert.equal(spaceRank('loopback') < spaceRank('private'), true);
    assert.equal(spaceRank('private') < spaceRank('public'), true);
  });

  it('flags only initiators more public than the target', () => {
    assert.equal(isMorePublic('public', 'private'), true);
    assert.equal(isMorePublic('public', 'loopback'), true);
    assert.equal(isMorePublic('private', 'loopback'), true);

    assert.equal(isMorePublic('private', 'public'), false);
    assert.equal(isMorePublic('loopback', 'private'), false);
    assert.equal(isMorePublic('public', 'public'), false);
    assert.equal(isMorePublic('private', 'private'), false);
  });
});
