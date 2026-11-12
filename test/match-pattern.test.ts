import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  InvalidMatchPatternError,
  globCovers,
  globMatches,
  matchesUrl,
  parseMatchPattern,
  patternCovers,
} from '../src/match-pattern.ts';

describe('parseMatchPattern', () => {
  it('parses a standard triple pattern', () => {
    const parsed = parseMatchPattern('https://*.example.com/api/*');
    assert.equal(parsed.scheme, 'https');
    assert.equal(parsed.host, '*.example.com');
    assert.equal(parsed.hostWildcard, true);
    assert.equal(parsed.path, '/api/*');
    assert.equal(parsed.allUrls, false);
  });

  it('parses <all_urls>', () => {
    const parsed = parseMatchPattern('<all_urls>');
    assert.equal(parsed.allUrls, true);
  });

  it('rejects malformed patterns', () => {
    assert.throws(() => parseMatchPattern('bogus'), InvalidMatchPatternError);
    assert.throws(() => parseMatchPattern('ws://example.com/*'), InvalidMatchPatternError);
    assert.throws(() => parseMatchPattern('https:///path'), InvalidMatchPatternError);
    assert.throws(() => parseMatchPattern('http://exa mple.com/*'), InvalidMatchPatternError);
    assert.throws(() => parseMatchPattern('https://example.com'), InvalidMatchPatternError);
  });

  it('only allows the bare * host with the * scheme', () => {
    assert.throws(() => parseMatchPattern('https://*/*'), InvalidMatchPatternError);
    assert.doesNotThrow(() => parseMatchPattern('*://*/*'));
  });
});

describe('matchesUrl - scheme', () => {
  it('the * scheme matches http and https only', () => {
    const pattern = '*://example.com/*';
    assert.equal(matchesUrl(pattern, 'http://example.com/'), true);
    assert.equal(matchesUrl(pattern, 'https://example.com/'), true);
    assert.equal(matchesUrl(pattern, 'ftp://example.com/'), false);
    assert.equal(matchesUrl(pattern, 'file:///etc/hosts'), false);
  });

  it('an explicit scheme does not match other schemes', () => {
    assert.equal(matchesUrl('https://example.com/*', 'http://example.com/'), false);
  });
});

describe('matchesUrl - *. subdomain', () => {
  const pattern = '*://*.example.com/*';

  it('covers the bare apex domain example.com', () => {
    assert.equal(matchesUrl(pattern, 'https://example.com/'), true);
    assert.equal(matchesUrl(pattern, 'http://example.com/users/1'), true);
  });

  it('covers nested subdomains', () => {
    assert.equal(matchesUrl(pattern, 'https://www.example.com/'), true);
    assert.equal(matchesUrl(pattern, 'https://a.b.example.com/deep/path'), true);
  });

  it('does not cover look-alike domains', () => {
    assert.equal(matchesUrl(pattern, 'https://notexample.com/'), false);
    assert.equal(matchesUrl(pattern, 'https://example.com.evil.test/'), false);
  });
});

describe('matchesUrl - path wildcard', () => {
  it('* matches any run including the empty string', () => {
    const pattern = 'https://shop.test/books/*';
    assert.equal(matchesUrl(pattern, 'https://shop.test/books/'), true);
    assert.equal(matchesUrl(pattern, 'https://shop.test/books/123'), true);
    assert.equal(matchesUrl(pattern, 'https://shop.test/books/123/chapter/4'), true);
    assert.equal(matchesUrl(pattern, 'https://shop.test/movies/123'), false);
  });

  it('ignores the query string and fragment', () => {
    assert.equal(matchesUrl('https://a.test/x', 'https://a.test/x?z=9#frag'), true);
  });
});

describe('matchesUrl - <all_urls>', () => {
  it('matches every http(s) URL', () => {
    assert.equal(matchesUrl('<all_urls>', 'https://anything.test/a/b/c'), true);
    assert.equal(matchesUrl('<all_urls>', 'http://localhost:3000/'), true);
  });
});

describe('globMatches / globCovers', () => {
  it('globMatches handles stars and literals', () => {
    assert.equal(globMatches('/a/*/c', '/a/b/c'), true);
    assert.equal(globMatches('/a/*/c', '/a/x/y/c'), true);
    assert.equal(globMatches('/*', '/anything'), true);
    assert.equal(globMatches('/exact', '/exact'), true);
    assert.equal(globMatches('/exact', '/exact/extra'), false);
  });

  it('globCovers is exact language inclusion', () => {
    assert.equal(globCovers('/*', '/api/v1/users'), true);
    assert.equal(globCovers('/api/*', '/api/v1/users'), true);
    assert.equal(globCovers('/api/v1/*', '/api/v2/users'), false);
    assert.equal(globCovers('/a/*/c', '/a/b/c'), true);
    assert.equal(globCovers('/a/b/c', '/a/*/c'), false);
    // Regex-style naive prefix checks would get this wrong.
    assert.equal(globCovers('/books/*', '/bookshelf/1'), false);
    assert.equal(globCovers('/*/x', '/a/b/x'), true);
    assert.equal(globCovers('/a/*/x', '/a/b/y'), false);
  });
});

describe('patternCovers', () => {
  it('detects scheme over-breadth', () => {
    assert.equal(patternCovers('*://example.com/*', 'https://example.com/*'), true);
    assert.equal(patternCovers('https://example.com/*', 'http://example.com/*'), false);
  });

  it('*://*.example.com/* covers a pattern pinned to the bare domain', () => {
    assert.equal(patternCovers('*://*.example.com/*', 'https://example.com/api/v2/*'), true);
    assert.equal(patternCovers('*://*.example.com/*', 'http://www.example.com/'), true);
  });

  it('a pinned host never covers a wildcard-host pattern', () => {
    assert.equal(patternCovers('https://example.com/*', 'https://*.example.com/*'), false);
  });

  it('detects path over-breadth', () => {
    assert.equal(patternCovers('https://a.test/api/*', 'https://a.test/api/v2/users/*'), true);
    assert.equal(patternCovers('https://a.test/api/v2/*', 'https://a.test/api/v1/*'), false);
  });

  it('<all_urls> covers everything but is not covered by anything else', () => {
    assert.equal(patternCovers('<all_urls>', 'https://anything.test/*'), true);
    assert.equal(patternCovers('*://*/*', '<all_urls>'), false);
  });

  it('rejects invalid input patterns', () => {
    assert.throws(() => patternCovers('not-a-pattern', 'https://a.test/*'), InvalidMatchPatternError);
  });
});
