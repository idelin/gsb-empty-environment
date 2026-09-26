import { StreamArrayValidator, ValidationError } from "../src/index.ts";
import type { Json, Schema } from "../src/index.ts";

const schema: Schema = {
  type: "array",
  minItems: 3,
  maxItems: 100_000,
  uniqueKeys: ["id", "profile.email"],
  aggregates: [{ key: "amount", sum: { minimum: 100, maximum: 1_000_000 } }],
  items: {
    type: "object",
    required: ["id", "amount", "profile"],
    properties: {
      id: { type: "integer", minimum: 1 },
      amount: { type: "number", minimum: 0 },
      profile: {
        type: "object",
        required: ["email"],
        properties: {
          email: { type: "string", minLength: 3, pattern: "@" },
          role: { type: "string", enum: ["admin", "member", "guest"] },
        },
      },
    },
  },
};

function line(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function printIssue(issue: { instancePath: string; rulePath: string; message: string }): void {
  console.log(`  instancePath=${issue.instancePath || "(root)"}  rulePath=${issue.rulePath}`);
  console.log(`    -> ${issue.message}`);
}

line("1. happy path: 3 items, global sum 330 within [100, 1000000]");
{
  const v = new StreamArrayValidator(schema);
  v.push({ id: 1, amount: 100, profile: { email: "a@x.io", role: "admin" } });
  v.push({ id: 2, amount: 200, profile: { email: "b@x.io", role: "member" } });
  v.push({ id: 3, amount: 30, profile: { email: "c@x.io" } });
  const result = v.finish();
  console.log(`  valid=${result.valid} length=${result.length} sums=${JSON.stringify(result.sums)}`);
}

line("2. 100,000 items: the 100,000th item repeats item #1's id (and nested email)");
{
  const v = new StreamArrayValidator(schema);
  const start = Date.now();
  for (let i = 1; i < 100_000; i++) {
    v.push({ id: i, amount: 1, profile: { email: `user${i}@x.io` } });
  }
  v.push({ id: 1, amount: 5, profile: { email: "user1@x.io" } });
  const result = v.finish();
  console.log(`  valid=${result.valid} length=${result.length} in ${Date.now() - start} ms`);
  result.issues.forEach(printIssue);
}

line("3. unfinished stream can NEVER report overall pass");
{
  const v = new StreamArrayValidator(schema);
  v.push({ id: 1, amount: 100, profile: { email: "a@x.io" } });
  console.log(`  status before finish(): ${JSON.stringify(v.status())}`);
  console.log("  (no result object exists until finish() -> minItems/aggregates unchecked)");
}

line("4. minItems can only be confirmed at finish()");
{
  const v = new StreamArrayValidator(schema);
  v.push({ id: 1, amount: 100, profile: { email: "a@x.io" } });
  console.log(`  pushed 1 valid item; status: ${JSON.stringify(v.status())}`);
  const result = v.finish();
  console.log(`  after finish(): valid=${result.valid}`);
  result.issues.forEach(printIssue);
}

line("5. onError='throw': first error stops the stream early");
{
  const v = new StreamArrayValidator(schema, "throw");
  v.push({ id: 1, amount: 100, profile: { email: "a@x.io" } });
  try {
    v.push({ id: -5, amount: -1, profile: { email: "bad" } } as unknown as Json);
  } catch (error) {
    if (error instanceof ValidationError) {
      console.log(`  threw ValidationError with ${error.issues.length} issue(s), stream stayed at length=${v.status().length}`);
      error.issues.forEach(printIssue);
    }
  }
}

line("6. onError='collect' (default): bad items are rejected, processing continues");
{
  const v = new StreamArrayValidator(schema);
  const first = v.push({ id: 1, amount: 100, profile: { email: "a@x.io" } });
  const bad = v.push({ amount: 50, profile: { role: "wizard" } } as unknown as Json);
  const dup = v.push({ id: 1, amount: 100, profile: { email: "a@x.io" } });
  const third = v.push({ id: 3, amount: 50, profile: { email: "c@x.io" } });
  const result = v.finish();
  console.log(`  push issues per call: ${[first.length, bad.length, dup.length, third.length].join(", ")}`);
  console.log(`  accepted length=${result.length} (bad item not counted), valid=${result.valid}`);
  result.issues.forEach(printIssue);
}

line("7. aggregate upper bound checked only at finish()");
{
  const tight: Schema = {
    items: { type: "object", properties: { amount: { type: "number" } } },
    aggregates: [{ key: "amount", sum: { maximum: 10 } }],
  };
  const v = new StreamArrayValidator(tight);
  v.push({ amount: 8 });
  console.log(`  after 8: ${JSON.stringify(v.status())} (still open)`);
  v.push({ amount: 5 });
  const result = v.finish();
  console.log(`  after finish(): valid=${result.valid}`);
  result.issues.forEach(printIssue);
}
