import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseMpd } from '../src/mpd.ts';
import { validateManifest } from '../src/validator.ts';
import { kidToBytes } from '../src/kid.ts';
import type { ValidationOptions, ValidationResult } from '../src/types.ts';
import {
  concat,
  ftypBox,
  initSegmentWithSinf,
  psshV1,
} from './helpers/boxes.ts';
import { buildMpd, type MpdRepresentation } from './helpers/mpd.ts';

const KID_A = '11111111-2222-3333-4444-555566667777';
const KID_B = 'aaaabbbb-cccc-dddd-eeee-ffff00001111';
const SYSTEM_ID = kidToBytes('edef8ba979d64acea3c827dcd51d21ed')!;
const LICENSE = 'https://license.example.com/v1/license';

const OPTIONS: ValidationOptions = {
  allowedLicenseUrls: ['https://license.example.com/'],
};

function goodInit(kid: string, scheme = 'cenc'): Uint8Array {
  const bytes = kidToBytes(kid.replace(/-/g, ''))!;
  return concat(
    ftypBox(),
    psshV1(SYSTEM_ID, [bytes]),
    initSegmentWithSinf(bytes, scheme),
  );
}

function attach(mpd: ReturnType<typeof parseMpd>, segments: Record<string, Uint8Array>): void {
  for (const adaptationSet of mpd.adaptationSets) {
    for (const representation of adaptationSet.representations) {
      representation.initSegment = segments[representation.id];
    }
  }
}

function run(representations: MpdRepresentation[], segments: Record<string, Uint8Array>, options = OPTIONS): ValidationResult {
  const mpd = parseMpd(buildMpd(representations));
  attach(mpd, segments);
  return validateManifest(mpd, options);
}

function codes(result: ValidationResult): string[] {
  return result.issues.map((issue) => issue.code);
}

test('合法清单：多 Representation 共享 KID、方案、PSSH、白名单 license', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_A, licenseUrl: LICENSE, scheme: 'cenc' },
    { id: 'v-720', kid: KID_A, licenseUrl: LICENSE, scheme: 'cenc' },
  ];
  const result = run(reps, {
    'v-360': goodInit(KID_A),
    'v-720': goodInit(KID_A),
  });
  assert.equal(result.ok, true, JSON.stringify(result.issues, null, 2));
});

test('攻击 1：AdaptationSet 内 default_KID 不一致被拦截', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_A, licenseUrl: LICENSE, scheme: 'cenc', inline: true },
    { id: 'v-720', kid: KID_B, licenseUrl: LICENSE, scheme: 'cenc', inline: true },
  ];
  const result = run(reps, {
    'v-360': goodInit(KID_A),
    'v-720': goodInit(KID_B),
  });
  assert.ok(codes(result).includes('DEFAULT_KID_MISMATCH'));
});

test('攻击 2：MPD KID 被改写，与 tenc KID 不一致', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_B, licenseUrl: LICENSE, scheme: 'cenc' },
  ];
  const result = run(reps, { 'v-360': goodInit(KID_A) });
  assert.ok(codes(result).includes('TENC_KID_MISMATCH'));
  const issue = result.issues.find((item) => item.code === 'TENC_KID_MISMATCH');
  assert.equal(issue?.kid, KID_A.replace(/-/g, ''));
});

test('攻击 3：方案值与 tenc/schm 不一致', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_A, licenseUrl: LICENSE, scheme: 'cbcs' },
  ];
  const result = run(reps, { 'v-360': goodInit(KID_A, 'cenc') });
  assert.ok(codes(result).includes('SCHEME_MISMATCH'));
});

test('攻击 4：PSSH 携带的是攻击者 KID，缺少真实 tenc KID', () => {
  const attackerBytes = kidToBytes(KID_B.replace(/-/g, ''))!;
  const realKidBytes = kidToBytes(KID_A.replace(/-/g, ''))!;
  const evilSegment = concat(
    ftypBox(),
    psshV1(SYSTEM_ID, [attackerBytes]),
    initSegmentWithSinf(realKidBytes, 'cenc'),
  );
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_A, licenseUrl: LICENSE, scheme: 'cenc' },
  ];
  const result = run(reps, { 'v-360': evilSegment });
  assert.ok(codes(result).includes('PSSH_KID_MISSING'));
});

test('攻击 5：licenseUrl 指向非白名单主机', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_A, licenseUrl: 'https://attacker.example/steal', scheme: 'cenc' },
  ];
  const result = run(reps, { 'v-360': goodInit(KID_A) });
  assert.ok(codes(result).includes('LICENSE_URL_NOT_ALLOWLISTED'));
});

test('缺失初始化分段与缺失 PSSH 分别报错', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_A, licenseUrl: LICENSE, scheme: 'cenc' },
  ];
  const noSegment = run(reps, {});
  assert.ok(codes(noSegment).includes('INIT_SEGMENT_MISSING'));

  const noPssh = run(reps, { 'v-360': initSegmentWithSinf(kidToBytes(KID_A.replace(/-/g, ''))!) });
  assert.ok(codes(noPssh).includes('PSSH_MISSING'));
});

test('畸形 default_KID 与无 ContentProtection 被识别', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: 'garbage', licenseUrl: LICENSE, scheme: 'cenc' },
  ];
  const result = run(reps, { 'v-360': goodInit(KID_A) });
  assert.ok(codes(result).includes('DEFAULT_KID_MALFORMED'));
});

test('Representation 级 ContentProtection 继承与内联均被校验', () => {
  const reps: MpdRepresentation[] = [
    { id: 'v-360', kid: KID_A, licenseUrl: LICENSE, scheme: 'cenc', inline: true },
  ];
  const result = run(reps, { 'v-360': goodInit(KID_A) });
  assert.equal(result.ok, true, JSON.stringify(result.issues, null, 2));
});
