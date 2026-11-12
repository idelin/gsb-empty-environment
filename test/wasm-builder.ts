export class WasmWriter {
  private readonly bytes: number[] = [];

  writeByte(byte: number): this {
    this.bytes.push(byte & 0xff);
    return this;
  }

  writeULEB(value: number): this {
    let remaining = value >>> 0;
    do {
      let byte = remaining & 0x7f;
      remaining >>>= 7;
      if (remaining !== 0) {
        byte |= 0x80;
      }
      this.writeByte(byte);
    } while (remaining !== 0);
    return this;
  }

  writeName(name: string): this {
    const encoded = new TextEncoder().encode(name);
    this.writeULEB(encoded.length);
    for (const byte of encoded) {
      this.writeByte(byte);
    }
    return this;
  }

  writeRaw(raw: number[]): this {
    for (const byte of raw) {
      this.writeByte(byte);
    }
    return this;
  }

  toUint8Array(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
}

const VAL = {
  i32: 0x7f,
  i64: 0x7e,
  f32: 0x7d,
  f64: 0x7c,
  v128: 0x7b,
  funcref: 0x70,
  externref: 0x6f,
} as const;

type ValueTypeName = keyof typeof VAL;

const SECTION_CUSTOM = 0;
const SECTION_TYPE = 1;
const SECTION_IMPORT = 2;
const SECTION_FUNCTION = 3;
const SECTION_START = 8;
const SECTION_CODE = 10;

function section(id: number, content: number[]): number[] {
  const header = new WasmWriter().writeByte(id).writeULEB(content.length);
  return [...header.toUint8Array(), ...content];
}

function funcType(params: ValueTypeName[], results: ValueTypeName[]): number[] {
  return [
    0x60,
    params.length,
    ...params.map((name) => VAL[name]),
    results.length,
    ...results.map((name) => VAL[name]),
  ];
}

export interface ImportSpec {
  module: string;
  field: string;
  kind: 'func' | 'memory' | 'table' | 'global';
  typeIndex?: number;
  limits?: { min: number; max: number | null; shared?: boolean };
  elementType?: ValueTypeName;
  valueType?: ValueTypeName;
  mutable?: boolean;
}

function importEntry(spec: ImportSpec): number[] {
  const out: number[] = [];
  const name = new WasmWriter().writeName(spec.module).writeName(spec.field);
  out.push(...name.toUint8Array());

  if (spec.kind === 'func') {
    out.push(0x00, spec.typeIndex ?? 0);
    return out;
  }

  if (spec.kind === 'memory') {
    const limits = spec.limits ?? { min: 1, max: null };
    const shared = limits.shared === true;
    const flags = (limits.max === null ? 0 : 1) | (shared ? 2 : 0);
    const encoded = new WasmWriter().writeByte(0x02).writeByte(flags).writeULEB(limits.min);
    if (limits.max !== null) {
      encoded.writeULEB(limits.max);
    }
    out.push(...encoded.toUint8Array());
    return out;
  }

  if (spec.kind === 'table') {
    const limits = spec.limits ?? { min: 1, max: null };
    const flags = limits.max === null ? 0 : 1;
    const encoded = new WasmWriter()
      .writeByte(0x01)
      .writeByte(VAL[spec.elementType ?? 'funcref'])
      .writeByte(flags)
      .writeULEB(limits.min);
    if (limits.max !== null) {
      encoded.writeULEB(limits.max);
    }
    out.push(...encoded.toUint8Array());
    return out;
  }

  out.push(0x03, VAL[spec.valueType ?? 'i32'], spec.mutable === true ? 1 : 0);
  return out;
}

export interface ModuleSpec {
  types?: Array<{ params: ValueTypeName[]; results: ValueTypeName[] }>;
  imports?: ImportSpec[];
  definedFunctions?: Array<{ typeIndex: number; body?: number[] }>;
  start?: number;
  customSection?: boolean;
}

export function buildWasm(spec: ModuleSpec): Uint8Array {
  const writer = new WasmWriter();
  writer.writeRaw([0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00]);

  if (spec.customSection) {
    const nameBytes = new WasmWriter().writeName('provenance').toUint8Array();
    writer.writeRaw(section(SECTION_CUSTOM, [...nameBytes, 1, 2, 3]));
  }

  const types = spec.types ?? [];
  if (types.length > 0) {
    const content = new WasmWriter()
      .writeULEB(types.length)
      .writeRaw(types.flatMap((type) => funcType(type.params, type.results)));
    writer.writeRaw(section(SECTION_TYPE, Array.from(content.toUint8Array())));
  }

  const imports = spec.imports ?? [];
  if (imports.length > 0) {
    const content = new WasmWriter()
      .writeULEB(imports.length)
      .writeRaw(imports.flatMap(importEntry));
    writer.writeRaw(section(SECTION_IMPORT, Array.from(content.toUint8Array())));
  }

  const definedFunctions = spec.definedFunctions ?? [];
  let codeSection: number[] = [];
  if (definedFunctions.length > 0) {
    const content = new WasmWriter()
      .writeULEB(definedFunctions.length)
      .writeRaw(definedFunctions.flatMap((func) => [func.typeIndex]));
    writer.writeRaw(section(SECTION_FUNCTION, Array.from(content.toUint8Array())));

    const bodies = definedFunctions.map((func) => {
      const instructions = func.body ?? [0x0b];
      const body = [0x00, ...instructions];
      return [body.length, ...body];
    });
    codeSection = section(SECTION_CODE, [definedFunctions.length, ...bodies.flat()]);
  }

  if (spec.start !== undefined) {
    writer.writeByte(SECTION_START).writeByte(1).writeULEB(spec.start);
  }

  writer.writeRaw(codeSection);

  return writer.toUint8Array();
}
