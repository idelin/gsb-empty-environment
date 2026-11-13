import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  validateDashManifest,
  extractKidsFromMpdPssh,
  base64ToBytes,
  parseMpd,
  validateManifest,
} from '../src/index.ts';
import { kidToBytes } from '../src/kid.ts';
import {
  concat,
  ftypBox,
  initSegmentWithSinf,
  psshV1,
} from './helpers/boxes.ts';
import { buildMpd, type MpdRepresentation } from './helpers/mpd.ts';

const KID = '11111111-2222-3333-4444-555566667777';
const SYSTEM_ID = kidToBytes('edef8ba979d64acea3c827dcd51d21ed')!;

test('validateDashManifest 端到端：拉取分段并返回 ok', async () => {
  const segment = concat(
    ftypBox(),
    psshV1(SYSTEM_ID, [kidToBytes(KID)!]),
    initSegmentWithSinf(kidToBytes(KID)!, 'cenc'),
  );
  const mpd = buildMpd([
    { id: 'r1', kid: KID, licenseUrl: 'https://license.example.com/l', scheme: 'cenc' },
  ]);
  const result = await validateDashManifest(mpd, {
    allowedLicenseUrls: ['https://license.example.com/'],
    fetchInitSegment: async () => segment,
  });
  assert.equal(result.ok, true, JSON.stringify(result.issues));
});

test('Base64 PSSH 解码后可提取内嵌 KID', () => {
  const pssh = psshV1(SYSTEM_ID, [kidToBytes(KID)!]);
  const base64 = toBase64(pssh);
  const bytes = base64ToBytes(base64);
  assert.equal(bytes.length, pssh.length);
  assert.ok(bytes.every((byte, index) => byte === pssh[index]));
  assert.deepEqual(extractKidsFromMpdPssh([base64]), [KID.replace(/-/g, '')]);
});

test('requireMpdPsshKid 在内联 PSSH 不含 KID 时报告问题', () => {
  const otherKid = kidToBytes('aaaabbbbccccddddeeeeffff00001111')!;
  const inlinePssh = toBase64(psshV1(SYSTEM_ID, [otherKid]));
  const reps: MpdRepresentation[] = [{
    id: 'r1',
    kid: KID,
    licenseUrl: 'https://license.example.com/l',
    scheme: 'cenc',
    psshBase64: inlinePssh,
    inline: true,
  }];
  const manifest = parseMpd(buildMpd(reps));
  manifest.adaptationSets[0].representations[0].initSegment = concat(
    ftypBox(),
    psshV1(SYSTEM_ID, [kidToBytes(KID)!]),
    initSegmentWithSinf(kidToBytes(KID)!, 'cenc'),
  );
  const result = validateManifest(manifest, {
    allowedLicenseUrls: ['https://license.example.com/'],
    requireMpdPsshKid: true,
  });
  assert.ok(result.issues.some((issue) => issue.code === 'PSSH_KID_MISSING'));
});

function toBase64(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
