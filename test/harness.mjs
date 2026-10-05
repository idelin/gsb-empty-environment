// Minimal test harness (Node 16 has no node:test).
const tests = [];

export function test(name, fn) {
  tests.push({ name, fn });
}

export async function run() {
  let pass = 0;
  let fail = 0;
  for (const t of tests) {
    try {
      await t.fn();
      pass++;
      console.log('ok   - ' + t.name);
    } catch (e) {
      fail++;
      console.error('FAIL - ' + t.name);
      console.error('       ' + String(e && e.stack ? e.stack : e).split('\n').join('\n       '));
    }
  }
  console.log('---');
  console.log(pass + ' passed, ' + fail + ' failed, ' + tests.length + ' total');
  if (fail > 0) {
    process.exit(1);
  }
}
