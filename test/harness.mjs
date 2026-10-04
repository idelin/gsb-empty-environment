const tests = [];

export function test(name, fn) {
  tests.push({ name, fn });
}

export async function run() {
  let failed = 0;
  for (const { name, fn } of tests) {
    try {
      await fn();
      console.log("ok   " + name);
    } catch (err) {
      failed++;
      console.error("FAIL " + name);
      console.error(err);
    }
  }
  console.log("");
  console.log((tests.length - failed) + "/" + tests.length + " tests passed");
  if (failed > 0) process.exit(1);
}
