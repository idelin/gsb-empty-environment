import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkLicenseUrl } from '../src/license.ts';

const ALLOWED = [
  'https://license.example.com/v1/license',
  'https://*.drm.example.org',
];

test('接受精确与子域通配白名单', () => {
  assert.equal(checkLicenseUrl('https://license.example.com/v1/license', ALLOWED).ok, true);
  assert.equal(checkLicenseUrl('https://acme.drm.example.org/acquire', ALLOWED).ok, true);
});

test('拒绝 http、错误域、路径越权与非白名单', () => {
  const http = checkLicenseUrl('http://license.example.com/v1/license', ALLOWED);
  assert.equal(http.ok, false);
  assert.equal(http.issue?.code, 'LICENSE_URL_NOT_HTTPS');

  const evil = checkLicenseUrl('https://license.evil.com/v1/license', ALLOWED);
  assert.equal(evil.issue?.code, 'LICENSE_URL_NOT_ALLOWLISTED');

  const bare = checkLicenseUrl('https://drm.example.org/acquire', ALLOWED);
  assert.equal(bare.issue?.code, 'LICENSE_URL_NOT_ALLOWLISTED');

  const suffixAttack = checkLicenseUrl('https://license.example.com.evil.com/v1/license', ALLOWED);
  assert.equal(suffixAttack.issue?.code, 'LICENSE_URL_NOT_ALLOWLISTED');

  const pathEscape = checkLicenseUrl('https://license.example.com/other/endpoint', ALLOWED);
  assert.equal(pathEscape.issue?.code, 'LICENSE_URL_NOT_ALLOWLISTED');
});

test('默认拒绝非 443 端口与内嵌凭据', () => {
  const port = checkLicenseUrl('https://license.example.com:8443/v1/license', ALLOWED);
  assert.equal(port.issue?.code, 'LICENSE_URL_NOT_ALLOWLISTED');
  assert.equal(
    checkLicenseUrl('https://license.example.com:8443/v1/license', ALLOWED, true).ok,
    true,
  );
  const creds = checkLicenseUrl('https://user:pass@license.example.com/v1/license', ALLOWED);
  assert.equal(creds.issue?.code, 'LICENSE_URL_MALFORMED');
});

test('缺失或畸形 URL 返回对应错误码', () => {
  assert.equal(checkLicenseUrl(undefined, ALLOWED).issue?.code, 'LICENSE_URL_MISSING');
  assert.equal(checkLicenseUrl('not-a-url', ALLOWED).issue?.code, 'LICENSE_URL_MALFORMED');
});
