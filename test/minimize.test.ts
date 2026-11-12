import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { minimalPatternSet, patternFromUrl, reducePatterns } from '../src/minimize.ts';

describe('patternFromUrl', () => {
  it('builds the tightest pattern for a concrete URL', () => {
    assert.equal(patternFromUrl('https://api.example.com/v2/users?id=1'), 'https://api.example.com/v2/users');
    assert.equal(patternFromUrl('http://example.com'), 'http://example.com/');
  });
});

describe('minimalPatternSet', () => {
  it('keeps a single exact requirement untouched', () => {
    assert.deepEqual(
      minimalPatternSet({ requiredUrls: ['https://api.example.com/v2/users'] }),
      ['https://api.example.com/v2/users'],
    );
  });

  it('drops requirements already covered by a wider declared pattern', () => {
    assert.deepEqual(
      minimalPatternSet({
        requiredPatterns: ['*://*.example.com/*', 'https://example.com/api/v2/*'],
      }),
      ['*://*.example.com/*'],
    );
  });

  it('merges multiple paths on one host into a common directory glob', () => {
    const result = minimalPatternSet({
      requiredUrls: [
        'https://cdn.example.com/assets/app.js',
        'https://cdn.example.com/assets/theme.css',
      ],
    });
    assert.deepEqual(result, ['https://cdn.example.com/assets/*']);
  });

  it('keeps unrelated hosts in separate patterns', () => {
    const result = minimalPatternSet({
      requiredUrls: ['https://a.test/x', 'https://b.test/y'],
    });
    assert.deepEqual(result, ['https://a.test/x', 'https://b.test/y']);
  });

  it('deduplicates identical requirements', () => {
    const result = minimalPatternSet({
      requiredUrls: ['https://a.test/x', 'https://a.test/x'],
    });
    assert.deepEqual(result, ['https://a.test/x']);
  });
});

describe('reducePatterns', () => {
  it('removes patterns subsumed by siblings', () => {
    const result = reducePatterns([
      'https://example.com/api/v2/*',
      '*://*.example.com/*',
      'https://other.test/*',
    ]);
    assert.deepEqual(result, ['*://*.example.com/*', 'https://other.test/*']);
  });

  it('keeps overlapping-but-distinct patterns', () => {
    const result = reducePatterns(['https://a.test/api/*', 'https://a.test/app/*']);
    assert.deepEqual(result, ['https://a.test/api/*', 'https://a.test/app/*']);
  });
});
