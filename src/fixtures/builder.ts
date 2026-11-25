import type {
  DistributionBundle,
  Manifest,
  SignatureRecord,
} from "../types.ts";
import { buildCanonicalSpec } from "../canonicalize.ts";
import { signToken } from "../crypto.ts";

/**
 * Fixed small-package layout shared by the acceptance scenarios.
 *
 * Byte layout (half-open offsets recorded in the manifest), total 50 bytes:
 *
 *   [0,12)  LEN-SUP=0011   length field governing the supplier region
 *   [12,22) SUP0123\r\nX   supplier region, CRLF fold at [19,21)
 *   [22,32) BLD;PAD\0X;    build region, removable padding at [29,30)
 *   [32,44) LEN-REL=0005   length field governing the release region
 *   [44,50) RELabc         release region, aliased under a second scope
 *
 * The two length fields are fixed 12-byte fields so the cross-field coverage
 * scenario has independent, non-adjacent length encodings.
 */
export const LAYOUT = {
  lenSupplier: { start: 0, end: 12 },
  supplier: { start: 12, end: 22 },
  build: { start: 22, end: 32 },
  lenRelease: { start: 32, end: 44 },
  release: { start: 44, end: 50 },
  fold: { start: 19, end: 21 },
  padding: { start: 29, end: 30 },
} as const;

export const PACKAGE_ID = "pkg-acceptance-0001";

export function buildRawBytes(): Uint8Array {
  return new TextEncoder().encode("LEN-SUP=0011SUP0123\r\nXBLD;PAD\0X;LEN-REL=0005RELabc");
}

export interface FixtureOptions {
  /** Canonical version stamped by the current manifest specs. */
  currentVersion?: string;
  /** Emit an ambiguous reverse map for the supplier canonicalization. */
  ambiguousSupplierMap?: boolean;
  /** Add the duplicate alias region for the release bytes. */
  includeAlias?: boolean;
  /** Domains that must never co-occur in one witness. */
  exclusionRules?: Manifest["exclusionRules"];
}

export function buildManifest(options: FixtureOptions = {}): Manifest {
  const rawBytes = buildRawBytes();
  const currentVersion = options.currentVersion ?? "2.0";

  const supplierCanon = buildCanonicalSpec(
    "canon-supplier",
    { ...LAYOUT.supplier },
    rawBytes,
    {
      version: currentVersion,
      foldNewlines: true,
      stripPadding: false,
      forceAmbiguous: options.ambiguousSupplierMap,
    },
  );
  const buildCanon = buildCanonicalSpec(
    "canon-build",
    { ...LAYOUT.build },
    rawBytes,
    { version: currentVersion, foldNewlines: false, stripPadding: true },
  );
  const releaseCanon = buildCanonicalSpec(
    "canon-release",
    { ...LAYOUT.release },
    rawBytes,
    { version: currentVersion, foldNewlines: false, stripPadding: false },
  );

  const regions: Manifest["regions"] = [
    { path: "regions/supplier.bin", range: { ...LAYOUT.supplier }, scope: "scope-supplier" },
    { path: "regions/build.bin", range: { ...LAYOUT.build }, scope: "scope-build" },
    { path: "regions/release.bin", range: { ...LAYOUT.release }, scope: "scope-release" },
  ];
  if (options.includeAlias) {
    regions.push({
      path: "aliases/release-link.bin",
      range: { ...LAYOUT.release },
      scope: "scope-alias",
      aliasOf: "regions/release.bin",
    });
  }

  return {
    packageId: PACKAGE_ID,
    canonicalizations: {
      [supplierCanon.id]: supplierCanon.spec,
      [buildCanon.id]: buildCanon.spec,
      [releaseCanon.id]: releaseCanon.spec,
    },
    regions,
    lengthFields: [
      { id: "len-supplier", range: { ...LAYOUT.lenSupplier }, regionPath: "regions/supplier.bin" },
      { id: "len-release", range: { ...LAYOUT.lenRelease }, regionPath: "regions/release.bin" },
    ],
    requirements: [
      {
        attribute: "vendor.identity",
        encodingRanges: [{ ...LAYOUT.supplier }],
        lengthFieldIds: ["len-supplier"],
        distinctKeyMaterials: 1,
      },
      {
        attribute: "build.provenance",
        encodingRanges: [{ ...LAYOUT.build }],
        lengthFieldIds: [],
        distinctKeyMaterials: 1,
      },
      {
        attribute: "release.policy",
        encodingRanges: [{ ...LAYOUT.release }],
        lengthFieldIds: ["len-release"],
        distinctKeyMaterials: 2,
      },
    ],
    schemaBindings: {
      "schema-supplier-v2": "canon-supplier",
      "schema-build-v2": "canon-build",
      "schema-release-v2": "canon-release",
      "schema-legacy-v1": "canon-supplier",
    },
    exclusionRules: options.exclusionRules ?? [
      { domainA: "domain-vendor", domainB: "domain-release" },
    ],
  };
}

export type RecordInput = Omit<SignatureRecord, "signatureBytes" | "index"> & {
  index?: number;
  validSignature?: boolean;
};

/** Build records, stamping deterministic simulated signature blobs. */
export function buildRecords(inputs: RecordInput[]): SignatureRecord[] {
  return inputs.map((input, position) => {
    const index = input.index ?? position;
    const { validSignature = true, ...rest } = input;
    const record: SignatureRecord = { ...rest, index, signatureBytes: new Uint8Array(0) };
    record.signatureBytes = validSignature ? signToken(record) : new TextEncoder().encode("bad");
    return record;
  });
}

export function buildBundle(
  records: SignatureRecord[],
  options: FixtureOptions = {},
): DistributionBundle {
  return {
    manifest: buildManifest(options),
    records,
    rawBytes: buildRawBytes(),
  };
}
