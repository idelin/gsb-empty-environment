import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseMpd } from '../src/mpd.ts';
import { buildMpd } from './helpers/mpd.ts';

const KID = '11111111-2222-3333-4444-555566667777';

test('解析 AdaptationSet 级 ContentProtection 并被 Representation 继承', () => {
  const mpd = parseMpd(buildMpd([
    { id: 'a', kid: KID, licenseUrl: 'https://license.example.com/l' },
    { id: 'b', kid: KID, licenseUrl: 'https://license.example.com/l' },
  ]));
  assert.equal(mpd.adaptationSets.length, 1);
  const adaptationSet = mpd.adaptationSets[0];
  assert.equal(adaptationSet.representations.length, 2);
  assert.equal(adaptationSet.contentProtections[0].defaultKids[0], `urn:uuid:${KID}`);
  assert.equal(adaptationSet.contentProtections[0].licenseUrl, 'https://license.example.com/l');
});

test('内联 Representation ContentProtection 与 cenc:pssh 文本被抽取', () => {
  const mpd = parseMpd(buildMpd([
    {
      id: 'a',
      kid: KID,
      licenseUrl: 'https://license.example.com/l',
      psshBase64: 'AAAA',
      inline: true,
    },
  ]));
  const rep = mpd.adaptationSets[0].representations[0];
  assert.equal(rep.contentProtections.length, 1);
  assert.deepEqual(rep.contentProtections[0].psshBoxesBase64, ['AAAA']);
});

test('多 Period 与实体解码、注释、CDATA 容错', () => {
  const xml = `<?xml version="1.0"?><!-- comment --><MPD xmlns="x">
    <Period id="0"><AdaptationSet id="a" contentType="video">
      <ContentProtection cenc:default_KID="urn:uuid:${KID}" dashif:laurl="https://x.test/l?a=1&amp;b=2"/>
      <Representation id="r1"><![CDATA[ignore]]></Representation>
    </AdaptationSet></Period>
    <Period id="1"><AdaptationSet id="b"><Representation id="r2"/></AdaptationSet></Period>
  </MPD>`;
  const mpd = parseMpd(xml);
  assert.equal(mpd.adaptationSets.length, 2);
  assert.equal(mpd.adaptationSets[0].contentProtections[0].licenseUrl, 'https://x.test/l?a=1&b=2');
  assert.equal(mpd.adaptationSets[1].id, 'b');
});
