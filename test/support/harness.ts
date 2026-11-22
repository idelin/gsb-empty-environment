import {
  ALL_RIGHTS,
  type EventKind,
  type FrontierSummary,
  type IdentityRecord,
  type KeyPair,
  type Right,
  type SignedEvent,
  type TrustRegistry,
  generateIdentityKeyPair,
  signEvent,
  signSummary,
} from "../../src/keyadmission.ts";
import type { Dot } from "../../src/dot.ts";

export interface Principal {
  readonly id: string;
  readonly keys: KeyPair;
  rights: Right[];
}

export class HarnessRegistry implements TrustRegistry {
  readonly principals = new Map<string, Principal>();
  private owners = new Map<string, string>();

  addPrincipal(id: string, rights: Right[] = []): Principal {
    const principal: Principal = { id, keys: generateIdentityKeyPair(), rights };
    this.principals.set(id, principal);
    return principal;
  }

  setOwner(objectId: string, principalId: string): void {
    this.owners.set(objectId, principalId);
  }

  ownerOf(objectId: string): string {
    const owner = this.owners.get(objectId);
    if (!owner) throw new Error(`no owner for ${objectId}`);
    return owner;
  }

  identity(actor: string): IdentityRecord | undefined {
    const principal = this.principals.get(actor);
    if (!principal) return undefined;
    return { publicKey: principal.keys.publicKey, rights: principal.rights };
  }

  actors(): string[] {
    return [...this.principals.keys()];
  }
}

/** Per-author local log builder with vector-clock style observed prefixes. */
export class LocalLog {
  private sequences = new Map<string, bigint>();
  private clocks = new Map<string, Map<string, bigint>>();

  private readonly registry: HarnessRegistry;

  constructor(registry: HarnessRegistry) {
    this.registry = registry;
  }

 tick(
    objectId: string,
    author: string,
    kind: EventKind,
    payload: SignedEvent["payload"],
    options: { generation?: number; observed?: Record<string, bigint>; sequence?: bigint } = {},
  ): SignedEvent {
    const sequence = options.sequence ?? (this.sequences.get(author) ?? 0n) + 1n;
    const previous = this.sequences.get(author) ?? 0n;
    this.sequences.set(author, sequence > previous ? sequence : previous);
    const clock = this.clocks.get(author) ?? new Map<string, bigint>();
    clock.set(author, sequence);
    for (const [observedActor, observedValue] of Object.entries(options.observed ?? {})) {
      const current = clock.get(observedActor);
      clock.set(observedActor, current !== undefined && current > observedValue ? current : observedValue);
    }
    this.clocks.set(author, clock);
    const observed: Record<string, bigint> = {};
    clock.forEach((value, observedActor) => {
      observed[observedActor] = value;
    });
    const principal = this.registry.principals.get(author)!;
    const body = {
      author,
      objectId,
      generation: options.generation ?? 0,
      dot: { actor: author, sequence },
      observed,
      kind,
      payload,
    };
    return signEvent(body, principal.keys.privateKey);
  }

  /** Fold another actor's latest prefix into this author's observed map. */
  observe(observer: string, other: string, sequence: bigint): void {
    const clock = this.clocks.get(observer) ?? new Map<string, bigint>();
    clock.set(other, clock.get(other) !== undefined && clock.get(other)! > sequence ? clock.get(other)! : sequence);
    this.clocks.set(observer, clock);
  }

  frontier(objectId: string, attester: string, entries: Record<string, bigint>): FrontierSummary {
    const principal = this.registry.principals.get(attester)!;
    return signSummary({ objectId, frontier: { ...entries }, attester }, principal.keys.privateKey);
  }

  sequenceOf(actor: string): bigint {
    return this.sequences.get(actor) ?? 0n;
  }
}

export function ownerRights(): Right[] {
  return [...ALL_RIGHTS];
}

export function shuffle<T>(items: readonly T[], seed: number): T[] {
  const out = [...items];
  let state = seed;
  for (let index = out.length - 1; index > 0; index -= 1) {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    const swap = state % (index + 1);
    [out[index], out[swap]] = [out[swap], out[index]];
  }
  return out;
}

export function dotOf(event: SignedEvent): Dot {
  return event.dot;
}
