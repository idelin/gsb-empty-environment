import { validateAndFill } from "../src/index.ts";
import type { JsonObject, Schema } from "../src/index.ts";

const userSchema: Schema = {
  type: "object",
  required: ["username", "email"],
  additionalProperties: false,
  properties: {
    username: { type: "string", minLength: 3 },
    email: { type: "string", pattern: "^[^@\\s]+@[^@\\s]+$" },
    role: { type: "string", enum: ["user", "admin"], default: "user" },
    displayName: { type: "string", default: { $from: "/username" } },
    profile: {
      type: "object",
      default: {},
      properties: {
        bio: { type: ["string", "null"], default: null },
        nickname: { type: "string", default: { $from: "/username" } },
      },
    },
    addresses: {
      type: "array",
      default: [],
      items: {
        type: "object",
        properties: {
          city: { type: "string" },
          country: { type: "string", default: "CN" },
          tags: { type: "array", default: [] },
        },
      },
    },
  },
};

function show(title: string, value: unknown): void {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(value, null, 2));
}

// 1. Happy path: missing fields are defaulted, $from chains resolve topologically.
const okInput = {
  username: "delin",
  email: "delin@example.com",
  addresses: [{ city: "Shanghai" }, { city: "Beijing", tags: ["work"] }],
};
const ok = validateAndFill(userSchema, okInput);
show("1. success: defaults filled", ok);

// The two address elements got independent default objects.
if (ok.ok) {
  const addresses = (ok.data as JsonObject).addresses as JsonObject[];
  (addresses[0].tags as unknown[]).push("home");
  console.log("\n   after mutating addresses[0].tags:", JSON.stringify(addresses[1].tags), "(element 1 unaffected)");
}

// 2. Validation failure: atomic rollback, input untouched, errors carry both paths.
const badInput: JsonObject = { username: "x", email: "not-an-email", unknownKey: true };
const snapshot = structuredClone(badInput);
const bad = validateAndFill(userSchema, badInput);
show("2. failure: rolled back, dual-path errors", bad);
console.log("\n   input unchanged after failure:", JSON.stringify(badInput) === JSON.stringify(snapshot));

// 3. Circular default dependencies report the chain.
const cyclicSchema: Schema = {
  type: "object",
  properties: {
    a: { type: "string", default: { $from: "/b" } },
    b: { type: "string", default: { $from: "/a" } },
  },
};
show("3. circular defaults: chain reported", validateAndFill(cyclicSchema, {}));

// 4. Explicit null is a value, not "missing".
const nullable = validateAndFill(userSchema, {
  username: "delin",
  email: "delin@example.com",
  profile: { bio: null },
});
show("4. explicit null is preserved (no default applied)", nullable.ok ? (nullable.data as JsonObject).profile : nullable);
