import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fromDefaults, fromJsonText } from '../src/sources.ts';
import { mergeLayers } from '../src/merge.ts';
import { explain, isSecretPath, redact, valueChanged, wasOverridden, SECRET_MASK } from '../src/secrets.ts';
import { provenanceAt } from '../src/provenance.ts';
import type { ConfigSchema } from '../src/types.ts';

const schema: ConfigSchema = {
  'db.password': { type: 'string', secret: true },
  'db.user': { type: 'string' },
};

function merged() {
  return mergeLayers([
    fromDefaults({ db: { user: 'app', password: 's3cret' }, port: 80 }),
    fromJsonText('{"db": {"password": "hunter2"}}', 'app.json'),
  ]);
}

test('redact masks secret fields but keeps others visible', () => {
  const result = merged();
  assert.deepEqual(redact(result.value, schema), {
    db: { user: 'app', password: SECRET_MASK },
    port: 80,
  });
});

test('raw value stays in the result and provenance for comparison', () => {
  const result = merged();
  assert.equal((result.value as any).db.password, 'hunter2');
  const info = provenanceAt(result.provenance, 'db.password')!;
  assert.equal(info.value, 'hunter2');
  assert.equal(info.overridden[0]!.value, 's3cret');
});

test('valueChanged compares raw secret values across merges', () => {
  const a = merged();
  const b = mergeLayers([
    fromDefaults({ db: { user: 'app', password: 's3cret' }, port: 80 }),
    fromJsonText('{"db": {"password": "hunter2"}}', 'app.json'),
  ]);
  const c = mergeLayers([fromDefaults({ db: { user: 'app', password: 's3cret' }, port: 80 })]);
  assert.equal(valueChanged(a.provenance, b.provenance, 'db.password'), false);
  assert.equal(valueChanged(a.provenance, c.provenance, 'db.password'), true);
  assert.equal(valueChanged(a.provenance, c.provenance, 'port'), false);
});

test('wasOverridden reports whether a higher layer replaced the value', () => {
  const result = merged();
  assert.equal(wasOverridden(result.provenance, 'db.password'), true);
  assert.equal(wasOverridden(result.provenance, 'port'), false);
});

test('explain masks secret values in the chain', () => {
  const result = merged();
  const text = explain(result.provenance, 'db.password', schema);
  assert.ok(!text.includes('hunter2'), 'secret must not leak');
  assert.ok(!text.includes('s3cret'), 'overridden secret must not leak');
  assert.match(text, new RegExp(`value=${SECRET_MASK.replace(/\*/g, '\\*')} from file`));
  assert.match(text, /overridden=\*{8} from defaults/);
});

test('isSecretPath matches nested descendants of a secret subtree', () => {
  const s: ConfigSchema = { tls: { type: 'object', secret: true } };
  assert.equal(isSecretPath(s, 'tls.key'), true);
  assert.equal(isSecretPath(s, 'tls'), true);
  assert.equal(isSecretPath(s, 'other'), false);
});
