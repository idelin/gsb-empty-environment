export type WasmValueType =
  | "i32"
  | "i64"
  | "f32"
  | "f64"
  | "v128"
  | "funcref"
  | "externref";

export type ImportKind = "function" | "table" | "memory" | "global";

export interface CapabilityIdentity {
  /** Canonical module name. `moduleName` is accepted as a compatibility alias. */
  module: string;
  /** Canonical field name. `name` is accepted as a compatibility alias. */
  field?: string;
  name?: string;
  fieldName?: string;
  moduleName?: string;
}

export interface FunctionCapability extends CapabilityIdentity {
  kind?: "function" | "func";
  params: readonly WasmValueType[];
  results: readonly WasmValueType[];
  /**
   * Imported functions are callable by module code after instantiation. This
   * extra opt-in is required when the same import is also the Wasm start
   * function, because start runs during instantiation.
   */
  allowStart?: boolean;
}

export interface MemoryCapability extends CapabilityIdentity {
  kind: "memory";
  min?: number;
  max?: number | null;
  shared?: boolean;
  memory64?: boolean;
}

export interface TableCapability extends CapabilityIdentity {
  kind: "table";
  elementType?: WasmValueType;
  min?: number;
  max?: number | null;
  table64?: boolean;
}

export interface GlobalCapability extends CapabilityIdentity {
  kind: "global";
  valueType?: WasmValueType;
  mutable?: boolean;
}

export type HostCapability =
  | FunctionCapability
  | MemoryCapability
  | TableCapability
  | GlobalCapability;

export type CapabilityTable = readonly HostCapability[];

export type ValidationErrorCode =
  | "INVALID_BINARY"
  | "INVALID_MAGIC"
  | "UNSUPPORTED_VERSION"
  | "UNKNOWN_SECTION"
  | "OUT_OF_ORDER_SECTION"
  | "DUPLICATE_SECTION"
  | "SECTION_SIZE_OUT_OF_RANGE"
  | "UNEXPECTED_TRAILING_BYTES"
  | "UNSUPPORTED_TYPE_FORM"
  | "UNSUPPORTED_VALUE_TYPE"
  | "INVALID_UTF8"
  | "INVALID_LIMITS"
  | "INVALID_IMPORT_KIND"
  | "IMPORT_TYPE_INDEX_OUT_OF_RANGE"
  | "FUNCTION_TYPE_INDEX_OUT_OF_RANGE"
  | "IMPORT_NOT_AUTHORIZED"
  | "IMPORT_KIND_MISMATCH"
  | "IMPORT_TYPE_MISMATCH"
  | "IMPORT_SIGNATURE_MISMATCH"
  | "START_FUNCTION_NOT_FOUND"
  | "START_SIGNATURE_INVALID"
  | "START_NOT_AUTHORIZED"
  | "INVALID_CAPABILITY";

export interface ValidationError {
  code: ValidationErrorCode;
  message: string;
  importIndex?: number;
  module?: string;
  field?: string;
}

export interface WasmFunctionType {
  params: WasmValueType[];
  results: WasmValueType[];
}

export interface WasmMemoryType {
  min: number;
  max: number | null;
  shared: boolean;
  memory64: boolean;
}

export interface WasmTableType {
  elementType: WasmValueType;
  min: number;
  max: number | null;
  table64: boolean;
}

export interface WasmGlobalType {
  valueType: WasmValueType;
  mutable: boolean;
}

export interface ValidatedImport {
  index: number;
  module: string;
  field: string;
  kind: ImportKind;
  functionType?: WasmFunctionType;
  memoryType?: WasmMemoryType;
  tableType?: WasmTableType;
  globalType?: WasmGlobalType;
  authorized: boolean;
}

export interface ValidatedStart {
  functionIndex: number;
  imported: boolean;
  module?: string;
  field?: string;
  authorized: boolean;
}

export interface ImportValidationResult {
  ok: boolean;
  /** Compatibility alias for `ok`. */
  valid: boolean;
  errors: ValidationError[];
  reason: string | null;
  imports: ValidatedImport[];
  start: ValidatedStart | null;
}

export type WasmBinary = Uint8Array | ArrayBuffer | ArrayBufferView;

interface RawLimits {
  min: number;
  max: number | null;
  shared: boolean;
  memory64: boolean;
}

interface RawImport {
  index: number;
  module: string;
  field: string;
  kind: ImportKind;
  typeIndex?: number;
  memoryType?: WasmMemoryType;
  tableType?: WasmTableType;
  globalType?: WasmGlobalType;
}

interface ParsedModule {
  types: WasmFunctionType[];
  imports: RawImport[];
  functionTypeIndices: number[];
  startFunctionIndex: number | null;
}

class WasmParseError extends Error {
  readonly code: ValidationErrorCode;

  constructor(
    code: ValidationErrorCode,
    message: string,
  ) {
    super(message);
    this.name = "WasmParseError";
    this.code = code;
  }
}

class Reader {
  private offset = 0;
  private readonly bytes: Uint8Array;
  private readonly context: string;

  constructor(
    bytes: Uint8Array,
    context = "module",
  ) {
    this.bytes = bytes;
    this.context = context;
  }

  get remaining(): number {
    return this.bytes.length - this.offset;
  }

  get eof(): boolean {
    return this.offset === this.bytes.length;
  }

  readU8(): number {
    if (this.remaining < 1) {
      throw this.error("unexpected end of input");
    }
    return this.bytes[this.offset++];
  }

  readBytes(length: number): Uint8Array {
    if (!Number.isSafeInteger(length) || length < 0 || length > this.remaining) {
      throw this.error("byte sequence is out of range");
    }
    const value = this.bytes.subarray(this.offset, this.offset + length);
    this.offset += length;
    return value;
  }

  readU32(): number {
    let result = 0;
    for (let index = 0; index < 5; index++) {
      const byte = this.readU8();
      if (index === 4 && (byte & 0xf0) !== 0) {
        throw this.error("u32 LEB128 value exceeds 32 bits");
      }
      result += (byte & 0x7f) * 2 ** (index * 7);
      if ((byte & 0x80) === 0) {
        return result >>> 0;
      }
    }
    throw this.error("u32 LEB128 value is too long");
  }

  readString(): string {
    const length = this.readU32();
    const value = this.readBytes(length);
    try {
      return new TextDecoder("utf-8", { fatal: true }).decode(value);
    } catch {
      throw new WasmParseError(
        "INVALID_UTF8",
        `${this.context}: string is not valid UTF-8`,
      );
    }
  }

  expectEnd(): void {
    if (!this.eof) {
      throw new WasmParseError(
        "UNEXPECTED_TRAILING_BYTES",
        `${this.context}: ${this.remaining} trailing byte(s)`,
      );
    }
  }

  private error(message: string): WasmParseError {
    return new WasmParseError("INVALID_BINARY", `${this.context}: ${message}`);
  }
}

const VALUE_TYPES: Readonly<Record<number, WasmValueType>> = {
  0x7b: "v128",
  0x7c: "f64",
  0x7d: "f32",
  0x7e: "i64",
  0x7f: "i32",
  0x70: "funcref",
  0x6f: "externref",
};

const SECTION_ORDER: Readonly<Record<number, number>> = {
  1: 1, // type
  2: 2, // import
  3: 3, // function
  4: 4, // table
  5: 5, // memory
  6: 6, // global
  7: 7, // export
  8: 8, // start
  9: 9, // element
  12: 10, // data count precedes code in the binary format
  10: 11, // code
  11: 12, // data
};

function readValueType(reader: Reader): WasmValueType {
  const byte = reader.readU8();
  const valueType = VALUE_TYPES[byte];
  if (valueType === undefined) {
    throw new WasmParseError(
      "UNSUPPORTED_VALUE_TYPE",
      `unsupported value type byte 0x${byte.toString(16)}`,
    );
  }
  return valueType;
}

function readValueTypeVector(reader: Reader): WasmValueType[] {
  const count = reader.readU32();
  if (count > reader.remaining) {
    throw new WasmParseError(
      "INVALID_BINARY",
      `value type vector count ${count} exceeds the remaining section`,
    );
  }
  const values: WasmValueType[] = [];
  for (let index = 0; index < count; index++) {
    values.push(readValueType(reader));
  }
  return values;
}

function readLimits(reader: Reader, kind: "memory" | "table"): RawLimits {
  const flags = reader.readU32();
  const allowedFlags = kind === "memory" ? 0x07 : 0x05;
  if (
    (flags & ~allowedFlags) !== 0 ||
    (kind === "table" && (flags & 0x02) !== 0)
  ) {
    throw new WasmParseError(
      "INVALID_LIMITS",
      `${kind} limits flags 0x${flags.toString(16)} are invalid`,
    );
  }

  const hasMax = (flags & 0x01) !== 0;
  const shared = (flags & 0x02) !== 0;
  const memory64 = (flags & 0x04) !== 0;
  if (shared && !hasMax) {
    throw new WasmParseError(
      "INVALID_LIMITS",
      "shared memory limits must include a maximum",
    );
  }

  const min = reader.readU32();
  const max = hasMax ? reader.readU32() : null;
  if (max !== null && max < min) {
    throw new WasmParseError(
      "INVALID_LIMITS",
      `${kind} maximum ${max} is smaller than minimum ${min}`,
    );
  }

  return { min, max, shared, memory64 };
}

function parseTypeSection(reader: Reader): WasmFunctionType[] {
  const count = reader.readU32();
  if (count > Math.floor(reader.remaining / 3)) {
    throw new WasmParseError(
      "INVALID_BINARY",
      `type count ${count} cannot fit in the section`,
    );
  }

  const types: WasmFunctionType[] = [];
  for (let index = 0; index < count; index++) {
    const form = reader.readU8();
    if (form !== 0x60) {
      throw new WasmParseError(
        "UNSUPPORTED_TYPE_FORM",
        `type #${index} has unsupported form byte 0x${form.toString(16)}`,
      );
    }
    types.push({
      params: readValueTypeVector(reader),
      results: readValueTypeVector(reader),
    });
  }
  reader.expectEnd();
  return types;
}

function parseImportSection(reader: Reader): RawImport[] {
  const count = reader.readU32();
  if (count > Math.floor(reader.remaining / 4)) {
    throw new WasmParseError(
      "INVALID_BINARY",
      `import count ${count} cannot fit in the section`,
    );
  }

  const imports: RawImport[] = [];
  for (let index = 0; index < count; index++) {
    const module = reader.readString();
    const field = reader.readString();
    const kindByte = reader.readU8();

    switch (kindByte) {
      case 0:
        imports.push({
          index,
          module,
          field,
          kind: "function",
          typeIndex: reader.readU32(),
        });
        break;
      case 1: {
        const elementType = readValueType(reader);
        if (elementType !== "funcref" && elementType !== "externref") {
          throw new WasmParseError(
            "INVALID_BINARY",
            `table import "${module}"."${field}" has non-reference element type ${elementType}`,
          );
        }
        const limits = readLimits(reader, "table");
        imports.push({
          index,
          module,
          field,
          kind: "table",
          tableType: {
            elementType,
            min: limits.min,
            max: limits.max,
            table64: limits.memory64,
          },
        });
        break;
      }
      case 2: {
        const limits = readLimits(reader, "memory");
        imports.push({
          index,
          module,
          field,
          kind: "memory",
          memoryType: {
            min: limits.min,
            max: limits.max,
            shared: limits.shared,
            memory64: limits.memory64,
          },
        });
        break;
      }
      case 3:
        imports.push({
          index,
          module,
          field,
          kind: "global",
          globalType: {
            valueType: readValueType(reader),
            mutable: readGlobalMutability(reader),
          },
        });
        break;
      default:
        throw new WasmParseError(
          "INVALID_IMPORT_KIND",
          `import #${index} has unsupported external kind ${kindByte}`,
        );
    }
  }
  reader.expectEnd();
  return imports;
}

function readGlobalMutability(reader: Reader): boolean {
  const mutability = reader.readU8();
  if (mutability !== 0 && mutability !== 1) {
    throw new WasmParseError(
      "INVALID_BINARY",
      `global mutability byte ${mutability} is invalid`,
    );
  }
  return mutability === 1;
}

function parseFunctionSection(reader: Reader): number[] {
  const count = reader.readU32();
  if (count > reader.remaining) {
    throw new WasmParseError(
      "INVALID_BINARY",
      `function count ${count} cannot fit in the section`,
    );
  }

  const typeIndices: number[] = [];
  for (let index = 0; index < count; index++) {
    typeIndices.push(reader.readU32());
  }
  reader.expectEnd();
  return typeIndices;
}

function parseStartSection(reader: Reader): number {
  const functionIndex = reader.readU32();
  reader.expectEnd();
  return functionIndex;
}

function parseModule(wasm: Uint8Array): ParsedModule {
  const reader = new Reader(wasm);
  const magic = reader.readBytes(4);
  if (
    magic[0] !== 0x00 ||
    magic[1] !== 0x61 ||
    magic[2] !== 0x73 ||
    magic[3] !== 0x6d
  ) {
    throw new WasmParseError("INVALID_MAGIC", "input is not a Wasm module");
  }

  const version = reader.readBytes(4);
  if (
    version[0] !== 0x01 ||
    version[1] !== 0x00 ||
    version[2] !== 0x00 ||
    version[3] !== 0x00
  ) {
    throw new WasmParseError(
      "UNSUPPORTED_VERSION",
      "only WebAssembly binary version 1 is supported",
    );
  }

  let types: WasmFunctionType[] = [];
  let imports: RawImport[] = [];
  let functionTypeIndices: number[] = [];
  let startFunctionIndex: number | null = null;
  let lastSectionOrder = 0;
  const seenSections = new Set<number>();

  while (!reader.eof) {
    const sectionId = reader.readU8();
    const sectionSize = reader.readU32();
    if (sectionSize > reader.remaining) {
      throw new WasmParseError(
        "SECTION_SIZE_OUT_OF_RANGE",
        `section ${sectionId} declares ${sectionSize} byte(s), but only ${reader.remaining} remain`,
      );
    }
    const payload = reader.readBytes(sectionSize);

    if (sectionId === 0) {
      continue;
    }

    const order = SECTION_ORDER[sectionId];
    if (order === undefined) {
      throw new WasmParseError(
        "UNKNOWN_SECTION",
        `unknown section id ${sectionId}`,
      );
    }
    if (seenSections.has(sectionId)) {
      throw new WasmParseError(
        "DUPLICATE_SECTION",
        `section ${sectionId} appears more than once`,
      );
    }
    if (order <= lastSectionOrder) {
      throw new WasmParseError(
        "OUT_OF_ORDER_SECTION",
        `section ${sectionId} is out of order`,
      );
    }
    seenSections.add(sectionId);
    lastSectionOrder = order;

    const sectionReader = new Reader(payload, `section ${sectionId}`);
    switch (sectionId) {
      case 1:
        types = parseTypeSection(sectionReader);
        break;
      case 2:
        imports = parseImportSection(sectionReader);
        break;
      case 3:
        functionTypeIndices = parseFunctionSection(sectionReader);
        break;
      case 8:
        startFunctionIndex = parseStartSection(sectionReader);
        break;
      default:
        // Sections outside the validation scope are skipped without traversing
        // their payloads. Their declared section boundaries were checked above.
        break;
    }
  }

  return { types, imports, functionTypeIndices, startFunctionIndex };
}

function capabilityModule(capability: HostCapability): string | undefined {
  if (capability === null || typeof capability !== "object") {
    return undefined;
  }
  return typeof capability.module === "string"
    ? capability.module
    : capability.moduleName;
}

function capabilityField(capability: HostCapability): string | undefined {
  if (capability === null || typeof capability !== "object") {
    return undefined;
  }
  if (typeof capability.field === "string") {
    return capability.field;
  }
  if (typeof capability.name === "string") {
    return capability.name;
  }
  return typeof capability.fieldName === "string"
    ? capability.fieldName
    : undefined;
}

function capabilityKind(capability: HostCapability): ImportKind {
  if (capability === null || typeof capability !== "object") {
    return "function";
  }
  return capability.kind === undefined || capability.kind === "func"
    ? "function"
    : capability.kind;
}

function sameStringArray(
  left: readonly string[],
  right: readonly string[],
): boolean {
  return (
    left.length === right.length &&
    left.every((value, index) => value === right[index])
  );
}

function sameFunctionType(
  actual: WasmFunctionType,
  capability: FunctionCapability,
): boolean {
  return (
    Array.isArray(capability.params) &&
    Array.isArray(capability.results) &&
    sameStringArray(actual.params, capability.params) &&
    sameStringArray(actual.results, capability.results)
  );
}

function sameOptional<T>(expected: T | undefined, actual: T): boolean {
  return expected === undefined || expected === actual;
}

function sameMemoryType(
  actual: WasmMemoryType,
  capability: MemoryCapability,
): boolean {
  return (
    sameOptional(capability.min, actual.min) &&
    (capability.max === undefined || capability.max === actual.max) &&
    sameOptional(capability.shared, actual.shared) &&
    sameOptional(capability.memory64, actual.memory64)
  );
}

function sameTableType(
  actual: WasmTableType,
  capability: TableCapability,
): boolean {
  return (
    sameOptional(capability.elementType, actual.elementType) &&
    sameOptional(capability.min, actual.min) &&
    (capability.max === undefined || capability.max === actual.max) &&
    sameOptional(capability.table64, actual.table64)
  );
}

function sameGlobalType(
  actual: WasmGlobalType,
  capability: GlobalCapability,
): boolean {
  return (
    sameOptional(capability.valueType, actual.valueType) &&
    sameOptional(capability.mutable, actual.mutable)
  );
}

function formatFunctionType(type: WasmFunctionType): string {
  return `(${type.params.join(", ")}) -> (${type.results.join(", ")})`;
}

function makeError(
  code: ValidationErrorCode,
  message: string,
  extra: Partial<ValidationError> = {},
): ValidationError {
  return { code, message, ...extra };
}

function capabilityError(
  capability: HostCapability,
  index: number,
): ValidationError | null {
  if (capabilityModule(capability) === undefined) {
    return makeError(
      "INVALID_CAPABILITY",
      `capability #${index} must have a string module name`,
    );
  }
  if (capabilityField(capability) === undefined) {
    return makeError(
      "INVALID_CAPABILITY",
      `capability #${index} must have a string field name`,
    );
  }
  if (
    capabilityKind(capability) === "function" &&
    (!Array.isArray((capability as FunctionCapability).params) ||
      !Array.isArray((capability as FunctionCapability).results))
  ) {
    return makeError(
      "INVALID_CAPABILITY",
      `function capability #${index} must declare params and results arrays`,
    );
  }
  return null;
}

function findCapabilities(
  capabilities: CapabilityTable,
  module: string,
  field: string,
): HostCapability[] {
  return capabilities.filter(
    (capability) =>
      capabilityModule(capability) === module &&
      capabilityField(capability) === field,
  );
}

function authorizeImport(
  rawImport: RawImport,
  type: WasmFunctionType | undefined,
  capabilities: CapabilityTable,
): {
  authorized: boolean;
  capability?: HostCapability;
  errors: ValidationError[];
} {
  const candidates = findCapabilities(
    capabilities,
    rawImport.module,
    rawImport.field,
  );
  const location = {
    importIndex: rawImport.index,
    module: rawImport.module,
    field: rawImport.field,
  };

  if (candidates.length === 0) {
    return {
      authorized: false,
      errors: [
        makeError(
          "IMPORT_NOT_AUTHORIZED",
          `import #${rawImport.index} "${rawImport.module}"."${rawImport.field}" is not granted by the capability table`,
          location,
        ),
      ],
    };
  }

  const sameKind = candidates.filter(
    (capability) => capabilityKind(capability) === rawImport.kind,
  );
  if (sameKind.length === 0) {
    return {
      authorized: false,
      errors: [
        makeError(
          "IMPORT_KIND_MISMATCH",
          `import #${rawImport.index} "${rawImport.module}"."${rawImport.field}" is ${rawImport.kind}, but the capability declares ${capabilityKind(candidates[0])}`,
          location,
        ),
      ],
    };
  }

  if (rawImport.kind === "function") {
    if (type === undefined) {
      return {
        authorized: false,
        errors: [
          makeError(
            "IMPORT_TYPE_INDEX_OUT_OF_RANGE",
            `import #${rawImport.index} has no valid function type`,
            location,
          ),
        ],
      };
    }
    const match = sameKind.find((capability) =>
      sameFunctionType(type, capability as FunctionCapability),
    );
    if (match === undefined) {
      const expected = (sameKind[0] as FunctionCapability).params;
      const expectedResults = (sameKind[0] as FunctionCapability).results;
      const expectedText =
        Array.isArray(expected) && Array.isArray(expectedResults)
          ? formatFunctionType({
              params: expected as WasmValueType[],
              results: expectedResults as WasmValueType[],
            })
          : "an invalid function type";
      return {
        authorized: false,
        errors: [
          makeError(
            "IMPORT_SIGNATURE_MISMATCH",
            `import #${rawImport.index} "${rawImport.module}"."${rawImport.field}" signature mismatch: expected ${expectedText}, got ${formatFunctionType(type)}`,
            location,
          ),
        ],
      };
    }
    return { authorized: true, capability: match, errors: [] };
  }

  let match: HostCapability | undefined;
  if (rawImport.kind === "memory") {
    match = sameKind.find((capability) =>
      sameMemoryType(
        rawImport.memoryType as WasmMemoryType,
        capability as MemoryCapability,
      ),
    );
  } else if (rawImport.kind === "table") {
    match = sameKind.find((capability) =>
      sameTableType(
        rawImport.tableType as WasmTableType,
        capability as TableCapability,
      ),
    );
  } else {
    match = sameKind.find((capability) =>
      sameGlobalType(
        rawImport.globalType as WasmGlobalType,
        capability as GlobalCapability,
      ),
    );
  }

  if (match === undefined) {
    return {
      authorized: false,
      errors: [
        makeError(
          "IMPORT_TYPE_MISMATCH",
          `import #${rawImport.index} "${rawImport.module}"."${rawImport.field}" ${rawImport.kind} type does not match the explicitly authorized type`,
          location,
        ),
      ],
    };
  }

  return { authorized: true, capability: match, errors: [] };
}

function describeImport(rawImport: RawImport): ValidatedImport {
  const result: ValidatedImport = {
    index: rawImport.index,
    module: rawImport.module,
    field: rawImport.field,
    kind: rawImport.kind,
    authorized: false,
  };
  if (rawImport.memoryType !== undefined) {
    result.memoryType = rawImport.memoryType;
  }
  if (rawImport.tableType !== undefined) {
    result.tableType = rawImport.tableType;
  }
  if (rawImport.globalType !== undefined) {
    result.globalType = rawImport.globalType;
  }
  return result;
}

function toUint8Array(input: WasmBinary): Uint8Array {
  if (input instanceof Uint8Array) {
    return input;
  }
  if (ArrayBuffer.isView(input)) {
    return new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
  }
  if (input instanceof ArrayBuffer) {
    return new Uint8Array(input);
  }
  throw new WasmParseError(
    "INVALID_BINARY",
    "input must be a Uint8Array, ArrayBuffer, or ArrayBuffer view",
  );
}

/**
 * Validates only the type, import, function, and start sections before a
 * plugin Wasm module is instantiated. Other section payloads are skipped.
 */
export function validateWasmImports(
  input: WasmBinary,
  capabilities: CapabilityTable,
): ImportValidationResult {
  const errors: ValidationError[] = [];
  const validatedImports: ValidatedImport[] = [];
  let validatedStart: ValidatedStart | null = null;

  let parsed: ParsedModule;
  try {
    parsed = parseModule(toUint8Array(input));
  } catch (error) {
    if (error instanceof WasmParseError) {
      errors.push(makeError(error.code, error.message));
    } else {
      errors.push(
        makeError(
          "INVALID_BINARY",
          error instanceof Error ? error.message : "failed to parse Wasm binary",
        ),
      );
    }
    return {
      ok: false,
      valid: false,
      errors,
      reason: errors.map((item) => `${item.code}: ${item.message}`).join("; "),
      imports: validatedImports,
      start: null,
    };
  }

  if (!Array.isArray(capabilities)) {
    errors.push(
      makeError("INVALID_CAPABILITY", "capability table must be an array"),
    );
  } else {
    capabilities.forEach((capability, index) => {
      const error = capabilityError(capability, index);
      if (error !== null) {
        errors.push(error);
      }
    });
  }

  const functionImports: RawImport[] = [];
  const importCapabilities = new Map<number, HostCapability>();

  for (const rawImport of parsed.imports) {
    const described = describeImport(rawImport);
    if (rawImport.kind === "function") {
      functionImports.push(rawImport);
      const type =
        rawImport.typeIndex === undefined
          ? undefined
          : parsed.types[rawImport.typeIndex];
      if (type !== undefined) {
        described.functionType = type;
      }
      if (type === undefined) {
        errors.push(
          makeError(
            "IMPORT_TYPE_INDEX_OUT_OF_RANGE",
            `import #${rawImport.index} references function type #${rawImport.typeIndex}, but only ${parsed.types.length} type(s) are present`,
            {
              importIndex: rawImport.index,
              module: rawImport.module,
              field: rawImport.field,
            },
          ),
        );
        validatedImports.push(described);
        continue;
      }
    }

    const authorization: {
      authorized: boolean;
      capability?: HostCapability;
      errors: ValidationError[];
    } = Array.isArray(capabilities)
      ? authorizeImport(rawImport, described.functionType, capabilities)
      : {
          authorized: false,
          errors: [],
        };
    described.authorized = authorization.authorized;
    errors.push(...authorization.errors);
    if (authorization.capability !== undefined) {
      importCapabilities.set(rawImport.index, authorization.capability);
    }
    validatedImports.push(described);
  }

  for (const [definedIndex, typeIndex] of parsed.functionTypeIndices.entries()) {
    if (parsed.types[typeIndex] === undefined) {
      errors.push(
        makeError(
          "FUNCTION_TYPE_INDEX_OUT_OF_RANGE",
          `defined function #${definedIndex} references function type #${typeIndex}, but only ${parsed.types.length} type(s) are present`,
        ),
      );
    }
  }

  if (parsed.startFunctionIndex !== null) {
    const startIndex = parsed.startFunctionIndex;
    const importedFunctionCount = functionImports.length;

    if (startIndex < importedFunctionCount) {
      const imported = functionImports[startIndex];
      const capability = importCapabilities.get(imported.index);
      const type =
        imported.typeIndex === undefined
          ? undefined
          : parsed.types[imported.typeIndex];
      const allowStart =
        capability !== undefined &&
        capabilityKind(capability) === "function" &&
        (capability as FunctionCapability).allowStart === true;

      validatedStart = {
        functionIndex: startIndex,
        imported: true,
        module: imported.module,
        field: imported.field,
        authorized: allowStart,
      };

      if (
        type !== undefined &&
        (type.params.length !== 0 || type.results.length !== 0)
      ) {
        validatedStart.authorized = false;
        errors.push(
          makeError(
            "START_SIGNATURE_INVALID",
            `start function #${startIndex} must have type () -> (), got ${formatFunctionType(type)}`,
            {
              importIndex: imported.index,
              module: imported.module,
              field: imported.field,
            },
          ),
        );
      }

      if (!allowStart) {
        errors.push(
          makeError(
            "START_NOT_AUTHORIZED",
            `start function #${startIndex} calls imported capability "${imported.module}"."${imported.field}" without explicit allowStart authorization`,
            {
              importIndex: imported.index,
              module: imported.module,
              field: imported.field,
            },
          ),
        );
      }
    } else {
      const definedFunctionIndex = startIndex - importedFunctionCount;
      const typeIndex = parsed.functionTypeIndices[definedFunctionIndex];
      const type =
        typeIndex === undefined ? undefined : parsed.types[typeIndex];

      if (typeIndex === undefined) {
        errors.push(
          makeError(
            "START_FUNCTION_NOT_FOUND",
            `start function index #${startIndex} is outside the module function index space`,
          ),
        );
        validatedStart = {
          functionIndex: startIndex,
          imported: false,
          authorized: false,
        };
      } else {
        const validSignature =
          type !== undefined &&
          type.params.length === 0 &&
          type.results.length === 0;
        validatedStart = {
          functionIndex: startIndex,
          imported: false,
          authorized: validSignature,
        };
        if (!validSignature) {
          errors.push(
            makeError(
              "START_SIGNATURE_INVALID",
              type === undefined
                ? `start function #${startIndex} has an unknown function type`
                : `start function #${startIndex} must have type () -> (), got ${formatFunctionType(type)}`,
            ),
          );
        }
      }
    }
  }

  const ok = errors.length === 0;
  return {
    ok,
    valid: ok,
    errors,
    reason: ok
      ? null
      : errors.map((item) => `${item.code}: ${item.message}`).join("; "),
    imports: validatedImports,
    start: validatedStart,
  };
}

export const validatePluginWasm = validateWasmImports;
