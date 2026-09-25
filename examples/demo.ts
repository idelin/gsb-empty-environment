/**
 * End-to-end runnable scenario.
 *
 *   node --experimental-strip-types examples/demo.ts
 *
 * It builds a corp root grant, delegates down three levels, and demonstrates:
 *   1. a valid full chain proof
 *   2. policy conjunction over the whole chain
 *   3. scope containment (a child cannot reach a resource it was not given)
 *   4. parent expiry invalidating a still-current leaf
 *   5. sealed (no-redelegate) enforcement
 *   6. depth exhaustion
 *   7. cyclic/return-delegation rejection
 *   8. ancestor revocation invalidating descendants
 *   9. proof tampering being detected by signature verification
 */
import { KeyDirectory } from "../src/keys.ts";
import { ManualClock } from "../src/clock.ts";
import { DelegationAuthority } from "../src/authority.ts";
import { DelegationError } from "../src/errors.ts";
import { and, attrIn, intCmp } from "../src/policy.ts";
import type { AuthzRequest, VerifyResult } from "../src/types.ts";

const T0 = 1_700_000_000; // fixed epoch second for deterministic output

const keys = new KeyDirectory();
for (const name of ["corp", "alice", "bob", "carol"]) keys.generatePrincipal(name);
const clock = new ManualClock(T0);
const authority = new DelegationAuthority(keys, clock);

function line(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function show(result: VerifyResult): void {
  console.log(result.allowed ? "decision: ALLOW" : "decision: DENY");
  if (!result.allowed) {
    for (const reason of result.reasons) {
      console.log(`  - ${reason.code}${reason.grantId ? ` @${reason.grantId.slice(0, 8)}` : ""}: ${reason.detail}`);
    }
  }
  console.log(
    "chain:  " +
      result.proof.chain
        .map((link) => `${link.grant.issuer}->${link.grant.subject}[depth=${link.grant.maxDepth}${link.grant.sealed ? ",sealed" : ""}]`)
        .join("  =>  "),
  );
}

function request(grantId: string, subject: string, resource: string, action: string, attrs: AuthzRequest["attrs"] = {}): VerifyResult {
  return authority.authorize({ grantId, subject, resource, action, attrs, at: clock.now() });
}

// Root: corp owns doc-a/doc-b, read/write, 3 delegation hops, 1000 seconds,
// and imposes an integer clearance condition for the entire chain.
line("1. corp issues a root grant to itself (the only power source)");
const root = authority.issueRoot("corp", {
  subject: "corp",
  resources: ["doc-a", "doc-b"],
  actions: ["read", "write"],
  maxDepth: 3,
  durationSeconds: 1000,
  policy: intCmp("clearance", ">=", 2),
});
console.log(`root grant ${root.grant.id.slice(0, 8)} expires at +1000s`);

// Alice gets read on doc-a, 500s, and adds an environment set condition.
line("2. corp delegates read/doc-a to alice");
const alice = authority.delegate("corp", root.grant.id, {
  subject: "alice",
  resources: ["doc-a"],
  actions: ["read"],
  durationSeconds: 500,
  policy: attrIn("env", ["prod", "staging"]),
});
console.log(`alice grant ${alice.grant.id.slice(0, 8)}, remaining depth ${alice.grant.maxDepth}`);

// Bob gets a sealed read grant on doc-a, 300s.
line("3. alice makes bob a sealed holder");
const bob = authority.delegate("alice", alice.grant.id, {
  subject: "bob",
  resources: ["doc-a"],
  actions: ["read"],
  sealed: true,
  durationSeconds: 300,
});
console.log(`bob grant ${bob.grant.id.slice(0, 8)} is sealed`);

line("4. bob reads doc-a with clearance=3 in prod -> ALLOW");
show(request(bob.grant.id, "bob", "doc-a", "read", { clearance: 3, env: "prod" }));

line("5. same call with clearance=1 -> DENY (ancestor integer policy)");
show(request(bob.grant.id, "bob", "doc-a", "read", { clearance: 1, env: "prod" }));

line("6. bob tries doc-b -> DENY (scope never expands downward)");
show(request(bob.grant.id, "bob", "doc-b", "read", { clearance: 3, env: "prod" }));

line("7. bob tries to redelegate the sealed grant -> rejected at issuance");
try {
  authority.delegate("bob", bob.grant.id, { subject: "carol", resources: ["doc-a"], actions: ["read"] });
} catch (error) {
  console.log(`rejected: ${(error as DelegationError).code} - ${(error as Error).message}`);
}

line("8. alice tries to grant carol deeper than the root allows -> rejected");
try {
  authority.delegate("alice", alice.grant.id, {
    subject: "carol",
    resources: ["doc-a"],
    actions: ["read"],
    maxDepth: alice.grant.maxDepth,
    durationSeconds: 100,
  });
} catch (error) {
  console.log(`rejected: ${(error as DelegationError).code} - ${(error as Error).message}`);
}

// Non-sealed branch to demonstrate time and revocation semantics.
line("9. alice delegates to carol on a second, short-lived branch");
const carol = authority.delegate("alice", alice.grant.id, {
  subject: "carol",
  resources: ["doc-a"],
  actions: ["read"],
  durationSeconds: 100,
  policy: and(attrIn("env", ["prod"])),
});
show(request(carol.grant.id, "carol", "doc-a", "read", { clearance: 5, env: "prod" }));

line("10. carol tries returning authority to corp -> cyclic delegation rejected");
try {
  authority.delegate("carol", carol.grant.id, {
    subject: "corp",
    resources: ["doc-a"],
    actions: ["read"],
  });
} catch (error) {
  console.log(`rejected: ${(error as DelegationError).code} - ${(error as Error).message}`);
}

line("11. clock advances past carol's grant while alice's is alive -> DENY (time)");
clock.set(T0 + 150);
show(request(carol.grant.id, "carol", "doc-a", "read", { clearance: 5, env: "prod" }));

line("12. clock advances past the alice grant; bob's grant is still inside its window -> DENY");
clock.set(T0 + 400);
show(request(bob.grant.id, "bob", "doc-a", "read", { clearance: 3, env: "prod" }));

line("13. tamper with a serialized proof (add doc-b at the leaf) -> signature fails");
clock.set(T0 + 10);
const good = authority.prove({
  grantId: bob.grant.id,
  subject: "bob",
  resource: "doc-a",
  action: "read",
  attrs: { clearance: 3, env: "prod" },
  at: clock.now(),
});
const tampered = JSON.parse(JSON.stringify(good)) as typeof good;
tampered.chain[tampered.chain.length - 1].grant.resources = ["doc-a", "doc-b"];
tampered.resource = "doc-b";
show(authority.verifyProof(tampered));

line("14. corp revokes alice's grant; bob (a descendant) stays invalid");
authority.revoke("corp", alice.grant.id);
show(request(bob.grant.id, "bob", "doc-a", "read", { clearance: 3, env: "prod" }));
