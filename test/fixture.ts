import { HmacSigner, type KeyEvent, type SignedEvent, type Dot } from "../src/keylog/index.ts";

/** Per-actor monotonic sequence counter for building histories in tests. */
export class Builder {
  readonly objectId: string;
  readonly signer: HmacSigner;
  readonly #seq = new Map<string, bigint>();

  constructor(objectId: string, keys: ReadonlyMap<string, Uint8Array>) {
    this.objectId = objectId;
    this.signer = new HmacSigner(keys);
  }

  next(actor: string): bigint {
    const value = (this.#seq.get(actor) ?? 0n) + 1n;
    this.#seq.set(actor, value);
    return value;
  }

  dot(actor: string, sequence: bigint): Dot {
    return { actor, sequence };
  }

  sign(event: KeyEvent): SignedEvent {
    return this.signer.sign(event);
  }

  rebuild(
    actor: string,
    generation: bigint,
    observed: Dot[],
    handle: string,
    sequence?: bigint,
  ): SignedEvent {
    return this.sign({
      kind: "rebuild",
      author: actor,
      objectId: this.objectId,
      generation,
      dot: { actor, sequence: sequence ?? this.next(actor) },
      observed,
      handle,
    });
  }

  delete(actor: string, generation: bigint, observed: Dot[], sequence?: bigint): SignedEvent {
    return this.sign({
      kind: "delete",
      author: actor,
      objectId: this.objectId,
      generation,
      dot: { actor, sequence: sequence ?? this.next(actor) },
      observed,
    });
  }

  grant(
    actor: string,
    generation: bigint,
    observed: Dot[],
    subject: string,
    handle: string,
    sequence?: bigint,
  ): SignedEvent {
    return this.sign({
      kind: "grant",
      author: actor,
      objectId: this.objectId,
      generation,
      dot: { actor, sequence: sequence ?? this.next(actor) },
      observed,
      subject,
      handle,
    });
  }

  delegate(
    actor: string,
    generation: bigint,
    observed: Dot[],
    subject: string,
    maxDepth: number,
    sequence?: bigint,
  ): SignedEvent {
    return this.sign({
      kind: "delegate",
      author: actor,
      objectId: this.objectId,
      generation,
      dot: { actor, sequence: sequence ?? this.next(actor) },
      observed,
      subject,
      maxDepth,
    });
  }

  revoke(
    actor: string,
    generation: bigint,
    observed: Dot[],
    targetDot: Dot,
    sequence?: bigint,
  ): SignedEvent {
    return this.sign({
      kind: "revoke",
      author: actor,
      objectId: this.objectId,
      generation,
      dot: { actor, sequence: sequence ?? this.next(actor) },
      observed,
      targetDot,
    });
  }
}

export function keyMap(actors: string[]): Map<string, Uint8Array> {
  const keys = new Map<string, Uint8Array>();
  for (const actor of actors) keys.set(actor, new TextEncoder().encode(`secret:${actor}`));
  return keys;
}

/** Deterministic PRNG (mulberry32) so delivery-order fuzzing is reproducible. */
export function rng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function shuffled<T>(items: T[], random: () => number): T[] {
  const copy = [...items];
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1));
    [copy[i], copy[j]] = [copy[j], copy[i]];
  }
  return copy;
}
