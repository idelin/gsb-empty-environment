import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import { ImportMapError, ImportMapGuard } from '../index.ts';

const ALLOWED = ['https://cdn.example.com', 'https://assets.example.com'];

const VALID_MAP = JSON.stringify({
  imports: {
    react: 'https://cdn.example.com/npm/react/index.js',
    'lodash/': 'https://cdn.example.com/npm/lodash/',
    'https://cdn.example.com/v1/app.js': 'https://cdn.example.com/v2/app.js',
  },
  scopes: {
    'https://cdn.example.com/plugins/override': {
      react: 'https://assets.example.com/scoped/react/index.js',
    },
    'https://cdn.example.com/plugins': {
      react: 'https://cdn.example.com/plugins/react.js',
    },
    'https://cdn.example.com/plugins/override/deep': {
      react: 'https://assets.example.com/deep/react.js',
    },
  },
});

describe('ImportMapGuard registration', () => {
  test('accepts exactly one well-formed map', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    const registered = guard.register(VALID_MAP);

    assert.equal(guard.hasMap(), true);
    assert.equal(registered.imports.length, 3);
    assert.deepEqual(
      registered.scopes.map((scope) => scope.prefix),
      [
        'https://cdn.example.com/plugins/override/deep/',
        'https://cdn.example.com/plugins/override/',
        'https://cdn.example.com/plugins/',
      ],
    );
  });

  test('rejects a second injected import map', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.throws(
      () => guard.register({ imports: { react: 'https://cdn.example.com/other.js' } }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'SECOND_MAP',
    );
  });

  test('closes registration once module resolution starts', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);
    guard.resolve('react');

    assert.throws(
      () => guard.register({ imports: {} }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'SECOND_MAP',
    );
  });

  test('rejects empty or invalid allow lists', () => {
    assert.throws(
      () => new ImportMapGuard({ allowedOrigins: [] }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_SHAPE',
    );
    assert.throws(
      () => new ImportMapGuard({ allowedOrigins: ['cdn.example.com'] }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_URL',
    );
    assert.throws(
      () => new ImportMapGuard({ allowedOrigins: ['https://cdn.example.com/path'] }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_URL',
    );
  });
});

describe('top-level imports resolution', () => {
  test('resolves exact and prefix specifiers', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.equal(
      guard.resolve('react'),
      'https://cdn.example.com/npm/react/index.js',
    );
    assert.equal(
      guard.resolve('lodash/fp/get.js'),
      'https://cdn.example.com/npm/lodash/fp/get.js',
    );
    assert.equal(
      guard.resolve('https://cdn.example.com/v1/app.js'),
      'https://cdn.example.com/v2/app.js',
    );
  });

  test('passes through absolute URLs that stay on an allowed origin', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.equal(
      guard.resolve('https://assets.example.com/vendor/lib.js'),
      'https://assets.example.com/vendor/lib.js',
    );
  });

  test('rejects unmapped bare specifiers', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.throws(
      () => guard.resolve('not-mapped'),
      (error: unknown) => error instanceof ImportMapError && error.code === 'NO_MATCH',
    );
  });

  test('rejects absolute specifiers on origins outside the allow list', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.throws(
      () => guard.resolve('https://evil.example/steal.js'),
      (error: unknown) => error instanceof ImportMapError && error.code === 'DISALLOWED_ORIGIN',
    );
    assert.throws(
      () => guard.resolve('data:text/javascript,alert(1)'),
      (error: unknown) => error instanceof ImportMapError && error.code === 'NO_MATCH',
    );
  });
});

describe('scope overrides with longest-prefix matching', () => {
  test('uses the longest matching scope before falling back to imports', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.equal(
      guard.resolve('react', 'https://cdn.example.com/plugins/override/deep/entry.js'),
      'https://assets.example.com/deep/react.js',
    );
    assert.equal(
      guard.resolve('react', 'https://cdn.example.com/plugins/override/entry.js'),
      'https://assets.example.com/scoped/react/index.js',
    );
    assert.equal(
      guard.resolve('react', 'https://cdn.example.com/plugins/entry.js'),
      'https://cdn.example.com/plugins/react.js',
    );
  });

  test('does not treat a partial path fragment as a scope boundary', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.equal(
      guard.resolve('react', 'https://cdn.example.com/plugins-evil/entry.js'),
      'https://cdn.example.com/npm/react/index.js',
    );
  });

  test('falls back to top-level imports when no scope contains the key', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.equal(
      guard.resolve('lodash/map.js', 'https://cdn.example.com/plugins/entry.js'),
      'https://cdn.example.com/npm/lodash/map.js',
    );
  });

  test('normalizes scope keys without a trailing slash before matching', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register({
      imports: {
        react: 'https://cdn.example.com/npm/react/index.js',
      },
      scopes: {
        'https://cdn.example.com/experiments': {
          react: 'https://assets.example.com/experiment/react.js',
        },
      },
    });

    assert.equal(
      guard.resolve('react', 'https://cdn.example.com/experiments/entry.js'),
      'https://assets.example.com/experiment/react.js',
    );
    assert.equal(
      guard.resolve('react', 'https://cdn.example.com/experiments-other/entry.js'),
      'https://cdn.example.com/npm/react/index.js',
    );
  });

  test('rejects importers outside the allow list', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register(VALID_MAP);

    assert.throws(
      () => guard.resolve('react', 'https://evil.example/page.js'),
      (error: unknown) => error instanceof ImportMapError && error.code === 'DISALLOWED_ORIGIN',
    );
  });
});

describe('trailing-slash normalization and escape prevention', () => {
  test('rejects targets outside the allow list', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });

    assert.throws(
      () =>
        guard.register({
          imports: { 'lodash/': 'https://evil.example/npm/lodash/' },
        }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'DISALLOWED_ORIGIN',
    );
  });

  test('rejects prefix keys whose target lacks a trailing slash', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });

    assert.throws(
      () =>
        guard.register({
          imports: { 'lodash/': 'https://cdn.example.com/npm/lodash/index.js' },
        }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'PREFIX_MISMATCH',
    );
  });

  test('blocks traversal that escapes the normalized prefix directory', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register({
      imports: { 'lodash/': 'https://cdn.example.com/npm/lodash/' },
    });

    assert.throws(
      () => guard.resolve('lodash/../../secret.js'),
      (error: unknown) => error instanceof ImportMapError && error.code === 'PREFIX_ESCAPE',
    );
  });

  test('keeps in-prefix traversal inside the normalized directory', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register({
      imports: { 'lodash/': 'https://cdn.example.com/npm/lodash/' },
    });

    assert.equal(
      guard.resolve('lodash/nested/../index.js'),
      'https://cdn.example.com/npm/lodash/nested/../index.js',
    );
  });

  test('rejects scope keys containing credentials', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });

    assert.throws(
      () =>
        guard.register({
          scopes: {
            'https://user:pass@cdn.example.com/experiments': {
              react: 'https://cdn.example.com/experiment/react.js',
            },
          },
        }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_URL',
    );
  });
});

describe('duplicate and prototype-polluting keys', () => {
  test('rejects duplicate JSON keys', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    const duplicateMap = `{
      "imports": {
        "react": "https://cdn.example.com/first.js",
        "react": "https://cdn.example.com/second.js"
      }
    }`;

    assert.throws(
      () => guard.register(duplicateMap),
      (error: unknown) => error instanceof ImportMapError && error.code === 'DUPLICATE_KEY',
    );
  });

  test('rejects __proto__ inside the JSON text', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    const protoMap = `{
      "imports": {
        "__proto__": { "polluted": true }
      }
    }`;

    assert.throws(
      () => guard.register(protoMap),
      (error: unknown) => error instanceof ImportMapError && error.code === 'PROTO_KEY',
    );
  });

  test('rejects __proto__ supplied through an object input', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    const entries = Object.create(null) as Record<string, string>;
    Object.defineProperty(entries, '__proto__', {
      value: 'https://cdn.example.com/polluted.js',
      enumerable: true,
      configurable: true,
      writable: true,
    });

    assert.throws(
      () =>
        guard.register({
          imports: entries,
        }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'PROTO_KEY',
    );
  });

  test('rejects __proto__ used as a scope key', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    const scopes = Object.create(null) as Record<string, Record<string, string>>;
    Object.defineProperty(scopes, '__proto__', {
      value: { react: 'https://cdn.example.com/polluted.js' },
      enumerable: true,
      configurable: true,
      writable: true,
    });

    assert.throws(
      () =>
        guard.register({
          scopes,
        }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'PROTO_KEY',
    );
  });
});

describe('longest prefix selection', () => {
  test('prefers the longest matching prefix mapping', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    guard.register({
      imports: {
        'https://cdn.example.com/': 'https://cdn.example.com/root/',
        'https://cdn.example.com/pkg/': 'https://cdn.example.com/pkg-mirror/',
        'https://cdn.example.com/pkg/sub/': 'https://assets.example.com/pkg-sub/',
      },
    });

    assert.equal(
      guard.resolve('https://cdn.example.com/pkg/sub/mod.js'),
      'https://assets.example.com/pkg-sub/mod.js',
    );
    assert.equal(
      guard.resolve('https://cdn.example.com/pkg/mod.js'),
      'https://cdn.example.com/pkg-mirror/mod.js',
    );
    assert.equal(
      guard.resolve('https://cdn.example.com/other.js'),
      'https://cdn.example.com/root/other.js',
    );
  });
});

describe('shape validation', () => {
  test('rejects scopes that collide after trailing-slash normalization', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });

    assert.throws(
      () =>
        guard.register({
          scopes: {
            'https://cdn.example.com/experiments': {
              react: 'https://cdn.example.com/a.js',
            },
            'https://cdn.example.com/experiments/': {
              react: 'https://cdn.example.com/b.js',
            },
          },
        }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_SHAPE',
    );
  });

  test('treats escaped and literal JSON key forms as duplicates', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });
    const escapedDuplicate = `{
      "imports": {
        "re\\u0061ct": "https://cdn.example.com/first.js",
        "react": "https://cdn.example.com/second.js"
      }
    }`;

    assert.throws(
      () => guard.register(escapedDuplicate),
      (error: unknown) => error instanceof ImportMapError && error.code === 'DUPLICATE_KEY',
    );
  });

  test('rejects malformed JSON', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });

    assert.throws(
      () => guard.register('{ "imports": '),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_JSON',
    );
  });

  test('rejects unknown top-level keys and non-string targets', () => {
    const guard = new ImportMapGuard({ allowedOrigins: ALLOWED });

    assert.throws(
      () => guard.register({ integrity: {} }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_SHAPE',
    );
    assert.throws(
      () => guard.register({ imports: { react: 42 } }),
      (error: unknown) => error instanceof ImportMapError && error.code === 'INVALID_SHAPE',
    );
  });
});
