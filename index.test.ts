import test from 'node:test';
import assert from 'node:assert/strict';

import {
  AnnotationValidationError,
  ConfigValidationError,
  getAnnotationValueError,
  inspectConfig,
  renderAnnotations,
  renderAnnotationsDetailed,
  validateAnnotationValue,
  validateConfig,
} from './index.ts';

const BODY_SIZE = 'example.com/proxy-body-size';
const TOKEN_HEADER = 'example.com/token-header';
const SAFE_PATH = 'example.com/safe-path';

const templates = [
  {
    annotation: BODY_SIZE,
    directive: 'client_max_body_size',
    type: 'size',
  },
  {
    annotation: TOKEN_HEADER,
    directive: 'proxy_set_header',
    type: 'token',
    template: 'proxy_set_header X-Feature {{value}};',
  },
  {
    annotation: SAFE_PATH,
    directive: 'proxy_pass',
    type: 'path',
  },
];

const whitelist = [
  'client_max_body_size',
  'proxy_set_header',
  'proxy_pass',
];

test('accepts a valid size and renders it', () => {
  assert.equal(validateAnnotationValue('10k', 'size'), true);
  assert.equal(validateAnnotationValue('10m', 'size'), true);
  assert.equal(getAnnotationValueError('10g', 'size') !== null, true);

  assert.equal(
    renderAnnotations({ [BODY_SIZE]: '10m' }, templates, whitelist),
    'client_max_body_size 10m;',
  );
});

test('rejects newline plus semicolon injection', () => {
  const malicious = '10m;\nload_module /tmp/evil.so;';

  assert.notEqual(getAnnotationValueError(malicious, 'size'), null);
  assert.throws(
    () => renderAnnotations({ [BODY_SIZE]: malicious }, templates, whitelist),
    AnnotationValidationError,
  );
});

test('rejects a right brace used to close the current block early', () => {
  const malicious = '10m} location /evil { proxy_pass http://evil; }#';

  assert.notEqual(getAnnotationValueError(malicious, 'size'), null);
  assert.throws(
    () => renderAnnotations({ [BODY_SIZE]: malicious }, templates, whitelist),
    AnnotationValidationError,
  );
});

test('rejects dollar variable references and comment prefixes', () => {
  assert.notEqual(getAnnotationValueError('$upstream_addr', 'token'), null);
  assert.notEqual(getAnnotationValueError('  # injected comment', 'token'), null);
});

test('rejects dot-dot path segments', () => {
  assert.equal(validateAnnotationValue('/api/v1', 'path'), true);
  assert.equal(validateAnnotationValue('/../etc/passwd', 'path'), false);
  assert.equal(validateAnnotationValue('/api/../private', 'path'), false);
  assert.equal(validateAnnotationValue('../etc/passwd', 'path'), false);

  assert.throws(
    () =>
      renderAnnotations(
        { [SAFE_PATH]: '/api/../private' },
        templates,
        whitelist,
      ),
    AnnotationValidationError,
  );
});

test('accepts token values only from the documented alphabet', () => {
  assert.equal(validateAnnotationValue('feature-1.on', 'token'), true);
  assert.equal(validateAnnotationValue('feature/one', 'token'), false);
  assert.equal(validateAnnotationValue('feature one', 'token'), false);
});

test('drops and logs unknown annotation keys', () => {
  const logs: string[] = [];
  const result = renderAnnotationsDetailed(
    { 'example.com/unknown': 'ignored' },
    templates,
    {
      allowedDirectives: whitelist,
      logger: (message) => logs.push(message),
    },
  );

  assert.equal(result.config, '');
  assert.deepEqual(result.droppedKeys, ['example.com/unknown']);
  assert.equal(logs.length, 1);
  assert.match(logs[0], /unknown/i);
});

test('state machine verifies blocks, parentheses and semicolons', () => {
  const config = [
    'location / {',
    '  proxy_pass http://upstream;',
    '  add_header X-Trace "semi;colon";',
    '}',
  ].join('\n');

  assert.equal(
    validateConfig(config, ['location', 'proxy_pass', 'add_header']),
    true,
  );
  assert.equal(inspectConfig(config).braceDepth, 0);
  assert.equal(inspectConfig(config).parenDepth, 0);

  assert.equal(validateConfig('location / { proxy_pass http://x; } }', []), false);
  assert.equal(validateConfig('location / { proxy_pass http://x;', []), false);
  assert.equal(validateConfig('location / { proxy_pass http://x }', []), false);
  assert.equal(validateConfig('if ($host { }', ['if']), false);
});

test('new directives must be present in the caller whitelist', () => {
  assert.equal(validateConfig('load_module /tmp/evil.so;', whitelist), false);

  const injectedTemplate = [
    {
      annotation: BODY_SIZE,
      directive: 'client_max_body_size',
      type: 'size',
      template: 'client_max_body_size {{value}};\nload_module /tmp/evil.so;',
    },
  ];

  assert.throws(
    () =>
      renderAnnotations(
        { [BODY_SIZE]: '10m' },
        injectedTemplate,
        ['client_max_body_size'],
      ),
    ConfigValidationError,
  );
});

test('supports annotation-keyed template objects', () => {
  const keyedTemplates = {
    'client_max_body_size': {
      annotation: BODY_SIZE,
      type: 'size',
    },
  };

  assert.equal(
    renderAnnotations(
      { [BODY_SIZE]: '8m' },
      keyedTemplates,
      ['client_max_body_size'],
    ),
    'client_max_body_size 8m;',
  );
});
