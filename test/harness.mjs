const tests = [];

export function test(name, fn) {
  tests.push({ name, fn });
}

export async function run() {
  let failed = 0;
  for (const t of tests) {
    try {
      await t.fn();
      console.log(`ok   ${t.name}`);
    } catch (err) {
      failed++;
      console.error(`FAIL ${t.name}`);
      console.error(err && err.stack ? err.stack : err);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) process.exitCode = 1;
}
