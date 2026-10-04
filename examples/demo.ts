import { writeFileSync } from 'node:fs';
import { loadConfig, explain } from '../src/index.ts';

writeFileSync('/tmp/app.config.json', JSON.stringify({
  server: { port: 8080 },
  tags: ['web'],
  db: { password: 'file-secret' },
}, null, 2));

const loaded = loadConfig({
  defaults: {
    server: { host: 'localhost', port: 80 },
    tags: ['base'],
    db: { user: 'app', password: 'default-secret' },
  },
  files: ['/tmp/app.config.json'],
  env: {
    prefix: 'APP_',
    schema: { 'server.port': { type: 'number' } },
    env: { APP_SERVER__PORT: '9090' },
  },
  cli: { argv: ['--tags={"$append":["prod"]}'], schema: {} },
  schema: {
    'server.port': { type: 'number' },
    'db.password': { type: 'string', secret: true },
  },
});

console.log('value:   ', JSON.stringify(loaded.value));
console.log('display: ', JSON.stringify(loaded.display));
console.log('issues:  ', loaded.issues);
console.log(explain(loaded.provenance, 'server.port'));
console.log(explain(loaded.provenance, 'db.password', { 'db.password': { type: 'string', secret: true } }));
