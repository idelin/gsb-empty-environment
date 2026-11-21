import { test } from 'node:test';
import assert from 'node:assert/strict';

import { globMatch, matchAnyGlob, segmentMatch } from '../src/matcher.ts';

test('globMatch supports literal equality', () => {
  assert.equal(globMatch('s3:GetObject', 's3:GetObject'), true);
  assert.equal(globMatch('s3:GetObject', 's3:PutObject'), false);
});

test('globMatch supports * and ? wildcards', () => {
  assert.equal(globMatch('s3:*', 's3:GetObject'), true);
  assert.equal(globMatch('arn:aws:s3:::bucket/*', 'arn:aws:s3:::bucket/a/b.png'), true);
  assert.equal(globMatch('arn:aws:s3:::bucket/*', 'arn:aws:s3:::other/a.png'), false);
  assert.equal(globMatch('arn:aws:s3:::bucket/?.txt', 'arn:aws:s3:::bucket/a.txt'), true);
  assert.equal(globMatch('arn:aws:s3:::bucket/?.txt', 'arn:aws:s3:::bucket/ab.txt'), false);
});

test('globMatch escapes regex metacharacters', () => {
  assert.equal(globMatch('a+b', 'a+b'), true);
  assert.equal(globMatch('a+b', 'axb'), false);
  assert.equal(globMatch('v1.0', 'v1.0'), true);
});

test('matchAnyGlob returns true when any pattern matches', () => {
  assert.equal(matchAnyGlob(['s3:Get*', 's3:List*'], 's3:ListBucket'), true);
  assert.equal(matchAnyGlob(['s3:Get*', 's3:List*'], 's3:DeleteObject'), false);
});

test('segmentMatch matches ARN colon segments independently', () => {
  assert.equal(
    segmentMatch('arn:aws:s3:*:1234:bucket/*', 'arn:aws:s3:us-east-1:1234:bucket/key'),
    true,
  );
  assert.equal(
    segmentMatch('arn:aws:s3:*:1234:bucket/*', 'arn:aws:s3:us-east-1:9999:bucket/key'),
    false,
  );
});
