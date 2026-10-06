// Minimal `node:test` stand-in for Node 16 (which lacks the built-in runner).
const tests = [];
export function test(name, fn) {
  tests.push([name, fn]);
}
export async function run() {
  let pass = 0;
  let fail = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      pass++;
      console.log(`ok ${pass + fail} - ${name}`);
    } catch (err) {
      fail++;
      console.log(`not ok ${pass + fail} - ${name}`);
      console.error(err);
    }
  }
  console.log(`# tests ${pass + fail}, pass ${pass}, fail ${fail}`);
  if (fail > 0) process.exitCode = 1;
}
