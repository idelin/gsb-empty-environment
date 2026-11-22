import {
  createManifest,
  encodeDeterministic,
  type AssetManifest,
  type PlaceholderSpec,
} from "../src/index.ts";

export function text(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

export function utf8(value: Uint8Array): string {
  return new TextDecoder().decode(value);
}

export function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.byteLength !== b.byteLength) {
    return false;
  }
  for (let index = 0; index < a.byteLength; index += 1) {
    if (a[index] !== b[index]) {
      return false;
    }
  }
  return true;
}

export interface FixtureOptions {
  readonly decoded: Uint8Array;
  readonly algorithms?: readonly string[];
  readonly transportAlgorithms?: readonly string[];
  readonly transform?: readonly string[];
  readonly placeholders?: readonly Partial<PlaceholderSpec>[];
  readonly shards?: number | readonly number[];
  readonly finalBytes?: Uint8Array;
  readonly decodedStage?: "auto" | "raw" | "final";
  readonly transportDomain?: string;
  readonly decodedDomain?: string;
}

export function fixture(options: FixtureOptions): {
  manifest: AssetManifest;
  transport: Uint8Array;
} {
  const transport = encodeDeterministic(options.decoded);
  const decodedForDigest =
    options.decodedStage === "final" && options.finalBytes !== undefined
      ? options.finalBytes
      : options.decoded;
  const manifest = createManifest({
    transportBytes: transport,
    decodedBytes: decodedForDigest,
    ...(options.finalBytes === undefined
      ? {}
      : { finalBytes: options.finalBytes }),
    transport: {
      domain: options.transportDomain ?? "test.transport.v1",
      algorithms: options.transportAlgorithms ?? options.algorithms ?? ["sha256"],
    },
    decoded: {
      domain: options.decodedDomain ?? "test.decoded.v1",
      algorithms: options.algorithms ?? ["sha256"],
    },
    transform: options.transform ?? ["rle"],
    placeholders: options.placeholders ?? [],
    shards: options.shards,
    ...(options.decodedStage === undefined
      ? {}
      : { decodedStage: options.decodedStage }),
  });
  return { manifest, transport };
}
