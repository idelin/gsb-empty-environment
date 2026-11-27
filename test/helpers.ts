import { InMemoryKeyRegistry, generateMemberKey, type MemberKey } from '../src/ownership/crypto.ts';
import type { RecoveryPolicy } from '../src/ownership/types.ts';

export interface Fixture {
  registry: InMemoryKeyRegistry;
  alice: MemberKey;
  bob: MemberKey;
  carol: MemberKey;
  dave: MemberKey;
  objectId: { origin: string; permanentId: string };
  policy: RecoveryPolicy;
}

export function buildFixture(): Fixture {
  const registry = new InMemoryKeyRegistry();
  const alice = generateMemberKey();
  const bob = generateMemberKey();
  const carol = generateMemberKey();
  const dave = generateMemberKey();
  for (const member of [alice, bob, carol, dave]) registry.add(member);
  const policy: RecoveryPolicy = {
    threshold: 2,
    members: [alice.keyId, bob.keyId, carol.keyId],
  };
  return {
    registry,
    alice,
    bob,
    carol,
    dave,
    objectId: { origin: 'origin.example', permanentId: 'obj-1' },
    policy,
  };
}

export function permutations<T>(input: readonly T[]): T[][] {
  if (input.length <= 1) return [[...input]];
  const result: T[][] = [];
  for (let index = 0; index < input.length; index += 1) {
    const head = input[index];
    const rest = [...input.slice(0, index), ...input.slice(index + 1)];
    for (const tail of permutations(rest)) result.push([head, ...tail]);
  }
  return result;
}

export function decisionMap(
  resolution: { decisions: readonly { eventHash: string; kind: string; reason: string }[] },
): Map<string, { kind: string; reason: string }> {
  const map = new Map<string, { kind: string; reason: string }>();
  for (const decision of resolution.decisions) {
    const existing = map.get(decision.eventHash);
    if (!existing || existing.reason === 'duplicate') map.set(decision.eventHash, decision);
  }
  return map;
}
