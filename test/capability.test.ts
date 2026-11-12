import assert from 'node:assert/strict';
import test from 'node:test';

import {
  CapabilityEngine,
  checkCapability,
  isLocalOrigin,
  matchPathPattern,
  matchRemoteUrl,
  matchWindowLabel,
  normalizePath,
  type Capability,
} from '../index.ts';

const env = {
  HOME: '/home/alice',
  APPDATA: '/home/alice/.config',
  USERPROFILE: 'C:\\Users\\alice',
};

function mainCapability(overrides: Partial<Capability> = {}): Capability {
  return {
    identifier: 'main',
    windows: ['main'],
    permissions: [
      {
        identifier: 'fs:allow-read-text-file',
        allow: ['$APPDATA/**'],
        deny: ['$APPDATA/secret/**'],
      },
    ],
    ...overrides,
  };
}

test('allows a file under $APPDATA and normalizes dot segments/repeated slashes', () => {
  const engine = new CapabilityEngine([mainCapability()], { env });

  for (const path of [
    '$APPDATA/notes.txt',
    '$APPDATA/./notes.txt',
    '$APPDATA//notes.txt',
    '//home//alice//.config//notes.txt',
    '${APPDATA}/notes.txt',
    '/home/alice/.config/a/../notes.txt',
  ]) {
    const decision = engine.check({
      windowLabel: 'main',
      permission: 'fs:allow-read-text-file',
      path,
      origin: 'tauri://localhost',
    });
    assert.equal(decision.allowed, true, `${path}: ${decision.message ?? ''}`);
  }
});

test('blocks .. traversal outside $APPDATA', () => {
  const engine = new CapabilityEngine([mainCapability()], { env });
  const decision = engine.check({
    windowLabel: 'main',
    permission: 'fs:allow-read-text-file',
    path: '$APPDATA/../../.ssh/id_rsa',
    origin: 'https://tauri.localhost',
  });

  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, 'path-not-allowed');
  assert.equal(decision.path, '/home/.ssh/id_rsa');
});

test('deny always wins, including when a path is disguised with dot segments', () => {
  const engine = new CapabilityEngine([mainCapability()], { env });

  for (const path of [
    '$APPDATA/secret/token.txt',
    '$APPDATA/secret/./token.txt',
    '$APPDATA//secret//token.txt',
    '/home/alice/.config/a/../secret/token.txt',
  ]) {
    const decision = engine.check({
      windowLabel: 'main',
      permission: 'fs:allow-read-text-file',
      path,
      origin: 'tauri://localhost',
    });
    assert.equal(decision.allowed, false, path);
    assert.equal(decision.reason, 'path-denied', path);
  }
});

test('deny from one capability overrides an allow from another', () => {
  const capabilities: Capability[] = [
    {
      identifier: 'allow-all',
      windows: ['*'],
      permissions: ['fs:read'],
    },
    {
      identifier: 'deny-secret',
      windows: ['main'],
      permissions: [
        {
          identifier: 'fs:read',
          deny: ['$APPDATA/secret/**'],
        },
      ],
    },
  ];
  const engine = new CapabilityEngine(capabilities, { env });

  assert.equal(
    engine.check({ windowLabel: 'main', permission: 'fs:read', path: '$APPDATA/ok' }).allowed,
    true,
  );
  assert.equal(
    engine.check({ windowLabel: 'main', permission: 'fs:read', path: '$APPDATA/secret/key' })
      .allowed,
    false,
  );
});

test('* does not cross a directory boundary while ** does', () => {
  assert.equal(matchPathPattern('$APPDATA/*', '$APPDATA/file.txt', { env }), true);
  assert.equal(matchPathPattern('$APPDATA/*', '$APPDATA/sub/file.txt', { env }), false);
  assert.equal(matchPathPattern('$APPDATA/**', '$APPDATA/sub/file.txt', { env }), true);
});

test('exact scopes do not match names with a trailing newline', () => {
  assert.equal(
    matchPathPattern('$APPDATA/notes.txt', '$APPDATA/notes.txt\n', { env }),
    false,
  );
  assert.equal(matchWindowLabel('main', 'main\n'), false);
});

test('every requested path must be covered by the union of allow scopes', () => {
  const engine = new CapabilityEngine(
    [
      {
        identifier: 'multi',
        windows: ['main'],
        permissions: [
          {
            identifier: 'fs:read-many',
            allow: ['$APPDATA/**', '$HOME/docs/**'],
          },
        ],
      },
    ],
    { env },
  );

  const allowed = engine.check({
    windowLabel: 'main',
    permission: 'fs:read-many',
    paths: ['%APPDATA%/a.txt', '$HOME/docs/b.txt'],
  });
  assert.equal(allowed.allowed, true);

  const denied = engine.check({
    windowLabel: 'main',
    permission: 'fs:read-many',
    paths: ['%APPDATA%/a.txt', '$HOME/.ssh/id_rsa'],
  });
  assert.equal(denied.allowed, false);
  assert.equal(denied.reason, 'path-not-allowed');
});

test('an explicit empty allow list grants nothing', () => {
  const engine = new CapabilityEngine(
    [
      {
        identifier: 'empty',
        windows: ['main'],
        permissions: [{ identifier: 'fs:read', allow: [] }],
      },
    ],
    { env },
  );
  assert.equal(
    engine.check({ windowLabel: 'main', permission: 'fs:read', path: '$APPDATA/a' }).allowed,
    false,
  );
});

test('an invalid deny scope fails closed instead of being ignored', () => {
  const engine = new CapabilityEngine(
    [
      {
        identifier: 'bad-deny',
        windows: ['main'],
        permissions: [
          {
            identifier: 'fs:read',
            allow: ['$APPDATA/**'],
            deny: ['$UNKNOWN/**'],
          },
        ],
      },
    ],
    { env },
  );
  const decision = engine.check({
    windowLabel: 'main',
    permission: 'fs:read',
    path: '$APPDATA/a',
  });
  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, 'path-denied');
});

test('remote origins must be paired with matching capabilities', () => {
  const engine = new CapabilityEngine(
    [
      mainCapability({
        remote: { urls: ['https://*.trusted.example', 'https://trusted.example/*'] },
      }),
    ],
    { env },
  );

  const check = (origin: string) =>
    engine.check({
      windowLabel: 'main',
      permission: 'fs:allow-read-text-file',
      path: '$APPDATA/notes.txt',
      origin,
    });

  assert.equal(check('https://app.trusted.example/page').allowed, true);
  assert.equal(check('https://trusted.example/file.txt').allowed, true);
  assert.equal(check('https://trusted.example/a/b').allowed, false);
  assert.equal(check('https://trusted.example.attacker.example/').allowed, false);
  assert.equal(check('https://attacker.example/').allowed, false);
});

test('local pages are exempt from remote.urls; missing origin is local', () => {
  const engine = new CapabilityEngine(
    [mainCapability({ remote: { urls: ['https://trusted.example'] } })],
    { env },
  );

  for (const origin of [
    'tauri://localhost/page',
    'http://tauri.localhost/',
    'file:///app/index.html',
  ]) {
    assert.equal(
      engine.check({
        windowLabel: 'main',
        permission: 'fs:allow-read-text-file',
        path: '$APPDATA/notes.txt',
        origin,
      }).allowed,
      true,
      origin,
    );
  }

  assert.equal(
    engine.check({
      windowLabel: 'main',
      permission: 'fs:allow-read-text-file',
      path: '$APPDATA/notes.txt',
      origin: 'https://trusted.example',
    }).allowed,
    true,
  );

  assert.equal(
    engine.check({
      windowLabel: 'main',
      permission: 'fs:allow-read-text-file',
      path: '$APPDATA/notes.txt',
    }).allowed,
    true,
  );
});

test('local-only capability does not grant a remote page', () => {
  const engine = new CapabilityEngine([mainCapability()], { env });
  const decision = engine.check({
    windowLabel: 'main',
    permission: 'fs:allow-read-text-file',
    path: '$APPDATA/notes.txt',
    origin: 'https://trusted.example',
  });

  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, 'remote-origin-denied');
  assert.equal(
    engine.check({
      windowLabel: 'main',
      permission: 'fs:allow-read-text-file',
      path: '$APPDATA/notes.txt',
      origin: 42 as unknown as string,
    }).reason,
    'remote-origin-denied',
  );
});

test('dynamically created window labels need a matching capability', () => {
  const capabilities: Capability[] = [
    {
      identifier: 'settings',
      windows: ['settings-*'],
      permissions: ['fs:read'],
    },
  ];
  const engine = new CapabilityEngine(capabilities, { env });

  assert.equal(
    engine.check({ windowLabel: 'settings-2', permission: 'fs:read', path: '$APPDATA/a' })
      .allowed,
    true,
  );

  const unknown = engine.check({
    windowLabel: 'attacker-window',
    permission: 'fs:read',
    path: '$APPDATA/a',
    origin: 'https://attacker.example',
  });
  assert.equal(unknown.allowed, false);
  assert.equal(unknown.reason, 'no-matching-capability');
});

test('unknown variable, relative path and pathless scoped permission fail closed', () => {
  const engine = new CapabilityEngine([mainCapability()], { env });

  const unknownVariable = engine.check({
    windowLabel: 'main',
    permission: 'fs:allow-read-text-file',
    path: '$UNKNOWN/file.txt',
  });
  assert.equal(unknownVariable.allowed, false);
  assert.equal(unknownVariable.reason, 'invalid-path');

  const relative = engine.check({
    windowLabel: 'main',
    permission: 'fs:allow-read-text-file',
    path: 'notes.txt',
  });
  assert.equal(relative.allowed, false);
  assert.equal(relative.reason, 'invalid-path');

  const noPath = engine.check({
    windowLabel: 'main',
    permission: 'fs:allow-read-text-file',
  });
  assert.equal(noPath.allowed, false);
  assert.equal(noPath.reason, 'path-required');

  const denyOnly = new CapabilityEngine(
    [
      {
        identifier: 'deny-only',
        windows: ['main'],
        permissions: [
          {
            identifier: 'fs:write',
            deny: ['$APPDATA/secret/**'],
          },
        ],
      },
    ],
    { env },
  );
  assert.equal(
    denyOnly.check({ windowLabel: 'main', permission: 'fs:write' }).reason,
    'path-required',
  );
});

test('exports embeddable helpers and a boolean convenience API', () => {
  assert.equal(matchWindowLabel('*', 'anything'), true);
  assert.equal(matchWindowLabel('main-*', 'main-2'), true);
  assert.equal(matchWindowLabel('main-*', 'other'), false);
  assert.equal(matchRemoteUrl('https://*.example.com', 'https://a.example.com/x'), true);
  assert.equal(matchRemoteUrl('https://*.example.com', 'https://example.com.evil.com/x'), false);
  assert.equal(isLocalOrigin('http://tauri.localhost/'), true);
  assert.equal(isLocalOrigin('https://attacker.example/'), false);

  const normalized = normalizePath('$APPDATA//a/../b', { env });
  assert.deepEqual(normalized, {
    root: '/',
    segments: ['home', 'alice', '.config', 'b'],
    value: '/home/alice/.config/b',
  });

  const capabilities: Capability[] = [mainCapability()];
  const allowed = checkCapability(
    capabilities,
    {
      windowLabel: 'main',
      permission: 'fs:allow-read-text-file',
      path: '$APPDATA/b',
    },
    { env },
  );
  assert.equal(allowed.allowed, true);
});

test('Windows paths are compared case-insensitively and keep the drive root', () => {
  const capabilities: Capability[] = [
    {
      identifier: 'win',
      windows: ['main'],
      permissions: [
        {
          identifier: 'fs:read',
          allow: ['$APPDATA/**'],
          deny: ['$APPDATA/Secret/**'],
        },
      ],
    },
  ];
  const engine = new CapabilityEngine(capabilities, {
    platform: 'windows',
    env: { APPDATA: 'C:\\Users\\Alice\\AppData\\Roaming' },
  });

  assert.equal(
    engine.check({
      windowLabel: 'main',
      permission: 'fs:read',
      path: 'c:/users/alice/appdata/roaming/notes.txt',
    }).allowed,
    true,
  );
  assert.equal(
    engine.check({
      windowLabel: 'main',
      permission: 'fs:read',
      path: 'C:\\Users\\Alice\\AppData\\Roaming\\secret\\x',
    }).allowed,
    false,
  );
});
