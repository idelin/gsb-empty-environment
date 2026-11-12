import { ByteReader, WasmParseError } from './binary.ts';
import type {
  FuncType,
  GlobalType,
  Limits,
  ParsedFunction,
  ParsedWasmModule,
  StartSection,
  TableType,
  ValueType,
  WasmImport,
} from './types.ts';

const WASM_MAGIC = [0x00, 0x61, 0x73, 0x6d];
const WASM_VERSION_1 = [0x01, 0x00, 0x00, 0x00];

const SECTION_TYPE = 1;
const SECTION_IMPORT = 2;
const SECTION_FUNCTION = 3;
const SECTION_START = 8;

const IMPORT_KIND_FUNC = 0;
const IMPORT_KIND_TABLE = 1;
const IMPORT_KIND_MEMORY = 2;
const IMPORT_KIND_GLOBAL = 3;

const TYPE_CODE: Record<number, ValueType> = {
  0x7f: 'i32',
  0x7e: 'i64',
  0x7d: 'f32',
  0x7c: 'f64',
  0x7b: 'v128',
  0x70: 'funcref',
  0x6f: 'externref',
};

export function parseWasmModule(bytes: Uint8Array): ParsedWasmModule {
  const reader = new ByteReader(bytes);

  const magic = [reader.u8(), reader.u8(), reader.u8(), reader.u8()];
  if (!magic.every((byte, index) => byte === WASM_MAGIC[index])) {
    throw new WasmParseError('input does not start with the wasm binary magic number');
  }
  const version = [reader.u8(), reader.u8(), reader.u8(), reader.u8()];
  if (!version.every((byte, index) => byte === WASM_VERSION_1[index])) {
    throw new WasmParseError('unsupported wasm binary version');
  }

  const types: FuncType[] = [];
  const imports: WasmImport[] = [];
  const functions: ParsedFunction[] = [];
  let start: StartSection | null = null;

  const seenSections = new Set<number>();
  let lastSectionId = -1;
  let importedFunctionCount = 0;

  while (!reader.eof()) {
    const sectionId = reader.u8();
    const sectionSize = reader.readULEB128();
    const payload = new ByteReader(reader.take(sectionSize));

    if (sectionId !== 0) {
      if (sectionId < lastSectionId) {
        throw new WasmParseError(`section ${sectionId} appears out of order`);
      }
      if (seenSections.has(sectionId)) {
        throw new WasmParseError(`section ${sectionId} appears more than once`);
      }
      lastSectionId = sectionId;
      seenSections.add(sectionId);
    }

    switch (sectionId) {
      case SECTION_TYPE:
        types.push(...parseTypeSection(payload));
        break;
      case SECTION_IMPORT:
        imports.push(...parseImportSection(payload, types, (typeIndex) => {
          const functionIndex = importedFunctionCount;
          importedFunctionCount += 1;
          return functionIndex;
        }));
        break;
      case SECTION_FUNCTION:
        functions.push(...parseFunctionSection(payload, types, importedFunctionCount));
        break;
      case SECTION_START:
        start = parseStartSection(payload);
        break;
      default:
        payload.take(payload.remaining);
        break;
    }

    if (sectionId !== 0 && !payload.eof()) {
      throw new WasmParseError(`section ${sectionId} contains trailing bytes`);
    }
  }

  return { types, imports, functions, start };
}

function parseTypeSection(reader: ByteReader): FuncType[] {
  const count = reader.readULEB128();
  const result: FuncType[] = [];
  for (let index = 0; index < count; index++) {
    const form = reader.u8();
    if (form !== 0x60) {
      throw new WasmParseError(`type entry ${index} is not a function type (0x60)`);
    }
    const paramCount = reader.readULEB128();
    const params: ValueType[] = [];
    for (let paramIndex = 0; paramIndex < paramCount; paramIndex++) {
      params.push(readValueType(reader));
    }
    const resultCount = reader.readULEB128();
    const results: ValueType[] = [];
    for (let resultIndex = 0; resultIndex < resultCount; resultIndex++) {
      results.push(readValueType(reader));
    }
    result.push({ params, results });
  }
  return result;
}

function parseImportSection(
  reader: ByteReader,
  types: FuncType[],
  allocateFunctionIndex: (typeIndex: number) => number,
): WasmImport[] {
  const count = reader.readULEB128();
  const result: WasmImport[] = [];
  for (let index = 0; index < count; index++) {
    const moduleName = reader.readName();
    const fieldName = reader.readName();
    const kind = reader.u8();
    switch (kind) {
      case IMPORT_KIND_FUNC: {
        const typeIndex = reader.readULEB128();
        if (typeIndex >= types.length) {
          throw new WasmParseError(
            `import ${moduleName}.${fieldName} references missing type index ${typeIndex}`,
          );
        }
        const functionIndex = allocateFunctionIndex(typeIndex);
        result.push({
          kind: 'func',
          module: moduleName,
          field: fieldName,
          typeIndex,
          functionIndex,
        });
        break;
      }
      case IMPORT_KIND_MEMORY:
        result.push({
          kind: 'memory',
          module: moduleName,
          field: fieldName,
          limits: readLimits(reader),
        });
        break;
      case IMPORT_KIND_TABLE:
        result.push({
          kind: 'table',
          module: moduleName,
          field: fieldName,
          tableType: readTableType(reader),
        });
        break;
      case IMPORT_KIND_GLOBAL:
        result.push({
          kind: 'global',
          module: moduleName,
          field: fieldName,
          globalType: readGlobalType(reader),
        });
        break;
      default:
        throw new WasmParseError(
          `import ${moduleName}.${fieldName} uses unknown import kind ${kind}`,
        );
    }
  }
  return result;
}

function parseFunctionSection(
  reader: ByteReader,
  types: FuncType[],
  importedFunctionCount: number,
): ParsedFunction[] {
  const count = reader.readULEB128();
  const result: ParsedFunction[] = [];
  for (let index = 0; index < count; index++) {
    const typeIndex = reader.readULEB128();
    if (typeIndex >= types.length) {
      throw new WasmParseError(`defined function ${index} references missing type index ${typeIndex}`);
    }
    result.push({
      index: importedFunctionCount + index,
      typeIndex,
      imported: false,
    });
  }
  return result;
}

function parseStartSection(reader: ByteReader): StartSection {
  return { functionIndex: reader.readULEB128() };
}

function readValueType(reader: ByteReader): ValueType {
  const code = reader.u8();
  const valueType = TYPE_CODE[code];
  if (valueType === undefined) {
    throw new WasmParseError(`unsupported value type encoding 0x${code.toString(16).padStart(2, '0')}`);
  }
  return valueType;
}

function readTableType(reader: ByteReader): TableType {
  const elementType = readValueType(reader);
  if (elementType !== 'funcref' && elementType !== 'externref') {
    throw new WasmParseError(`table element type must be funcref or externref, got ${elementType}`);
  }
  return { elementType, limits: readLimits(reader) };
}

function readGlobalType(reader: ByteReader): GlobalType {
  const valueType = readValueType(reader);
  const mutability = reader.u8();
  if (mutability !== 0 && mutability !== 1) {
    throw new WasmParseError(`global mutability flag must be 0 or 1, got ${mutability}`);
  }
  return { valueType, mutable: mutability === 1 };
}

function readLimits(reader: ByteReader): Limits {
  const flags = reader.u8();
  if (flags > 3) {
    throw new WasmParseError(`unsupported limits flags 0x${flags.toString(16)}`);
  }
  const min = reader.readULEB128();
  const hasMaximum = (flags & 1) === 1;
  const max = hasMaximum ? reader.readULEB128() : null;
  const shared = (flags & 2) === 2;
  if (max !== null && max < min) {
    throw new WasmParseError('limits maximum is smaller than the minimum');
  }
  return { min, max, shared };
}
