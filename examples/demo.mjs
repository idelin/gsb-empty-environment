import { migrate, unmigrate, rename, split, merge } from '../dist/index.js';

// A realistic v1 -> v2 ruleset:
//  - rename:      `env`            -> `environment`          (1:1, reversible)
//  - split:       `address`        -> `hostname` + `port`    (1:N, reversible)
//  - merge:       `timeout`+`unit` -> `timeoutMs`            (N:1, unit is lossy)
//  - one-way:     `password`       -> `passwordHash`         (not reversible)
const rules = [
  rename('rename-env', 'env', 'environment', { priority: 10 }),
  split(
    'split-address',
    'address',
    ['hostname', 'port'],
    (value) => {
      const [hostname, port] = String(value).split(':');
      return { hostname, port: Number(port) };
    },
    (values) => `${values.hostname}:${values.port}`,
  ),
  merge(
    'merge-timeout',
    ['timeout', 'timeoutUnit'],
    'timeoutMs',
    (values) => {
      const factors = { ms: 1, s: 1000 };
      return values.timeout * (factors[values.timeoutUnit ?? 'ms'] ?? 1);
    },
    (value) => ({ timeout: value }),
  ),
  {
    id: 'hash-password',
    oldKeys: ['password'],
    newKeys: ['passwordHash'],
    forward: (old) => ({ values: { passwordHash: `sha256(${old.password})` } }),
  },
];

const legacy = {
  env: 'prod',
  address: 'example.com:8080',
  timeout: 30,
  timeoutUnit: 's',
  password: 's3cret',
  legacyUnknown: 'nobody consumes me',
};

const result = migrate(legacy, rules);
console.log('--- migrated config ---');
console.log(result.config);
console.log('--- provenance (new key <- sources) ---');
for (const p of result.provenance) {
  console.log(`  ${p.key}  <-  [${p.sources.join(', ')}]  (rule: ${p.ruleId})`);
}
console.log('--- losses ---');
console.log(result.losses);
console.log('--- unconsumed old fields ---');
console.log(result.unconsumed);

console.log('--- re-migrate is stable ---');
const again = migrate(result.config, rules);
console.log('same config:', JSON.stringify(again.config) === JSON.stringify(result.config));
console.log('rules applied on 2nd run:', again.appliedRules);

console.log('--- unmigrate (reverse the reversible part) ---');
const back = unmigrate(result.config, rules);
console.log(back.config);
console.log('losses on the way back:', back.losses.map((l) => `${l.ruleId}: ${l.reason}`));
