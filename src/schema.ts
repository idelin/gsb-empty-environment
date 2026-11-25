import { createHash } from 'node:crypto';
import {
  DecodeError,
  type FieldDef,
  type ObjectTypeDef,
  type TypeDef,
  type TypeGraph,
} from './types.ts';

export interface CompiledField {
  no: number;
  names: string[];
  typeNo: number;
  required: boolean;
}

export interface CompiledObject {
  kind: 'object';
  no: number;
  fieldsByNo: Map<number, CompiledField>;
  /** JSON 属性名（含别名）-> 字段编号。 */
  fieldNoByName: Map<string, number>;
  sortedFields: CompiledField[];
}

export interface CompiledArray {
  kind: 'array';
  no: number;
  elementTypeNo: number;
}

export interface CompiledScalar {
  kind: 'id64' | 'int32' | 'string' | 'bool';
  no: number;
}

export type CompiledType = CompiledObject | CompiledArray | CompiledScalar;

export interface CompiledMigration {
  fromVersion: number;
  movesByNo: Map<number, number[]>;
  movesByName: Map<string, { fromFieldNo: number; toPath: number[] }>;
}

export interface CompiledGraph {
  raw: TypeGraph;
  rootTypeNo: number;
  version: number;
  digest: string;
  types: Map<number, CompiledType>;
  migrations: Map<number, CompiledMigration>;
}

const textEncoder = new TextEncoder();

export function compileGraph(graph: TypeGraph): CompiledGraph {
  const types = new Map<number, CompiledType>();
  if (!graph || typeof graph !== 'object' || !graph.types) {
    throw new DecodeError('SCHEMA_INVALID', 'missing types');
  }

  for (const typeNo of Object.keys(graph.types)) {
    const def = graph.types[Number(typeNo)];
    types.set(def.no, compileType(def, graph));
  }

  if (!types.has(graph.rootTypeNo)) {
    throw new DecodeError('SCHEMA_INVALID', `root type ${graph.rootTypeNo} not found`);
  }

  // 引用完整性 + 递归可达性检查。
  for (const compiled of types.values()) {
    if (compiled.kind === 'array' && !types.has(compiled.elementTypeNo)) {
      throw new DecodeError('SCHEMA_INVALID', `array ${compiled.no} element missing`);
    }
    if (compiled.kind === 'object') {
      for (const field of compiled.sortedFields) {
        if (!types.has(field.typeNo)) {
          throw new DecodeError('SCHEMA_INVALID', `field type ${field.typeNo} missing`);
        }
      }
    }
  }

  const migrations = new Map<number, CompiledMigration>();
  const rootType = types.get(graph.rootTypeNo);
  for (const migration of graph.migrations ?? []) {
    const movesByNo = new Map<number, number[]>();
    const movesByName = new Map<string, { fromFieldNo: number; toPath: number[] }>();
    const seenSources = new Set<number>();
    for (const move of migration.moves) {
      if (move.toPath.length === 0) {
        throw new DecodeError('SCHEMA_INVALID', 'migration toPath must be non-empty');
      }
      if (seenSources.has(move.fromFieldNo)) {
        throw new DecodeError(
          'MIGRATION_CONFLICT',
          `duplicate migration source field ${move.fromFieldNo}`,
        );
      }
      seenSources.add(move.fromFieldNo);
      if (typeof move.fromName !== 'string' || move.fromName.length === 0) {
        throw new DecodeError('SCHEMA_INVALID', 'migration move needs fromName');
      }
      if (movesByName.has(move.fromName)) {
        throw new DecodeError(
          'MIGRATION_CONFLICT',
          `duplicate migration source name "${move.fromName}"`,
        );
      }
      validateMigrationPath(types, graph.rootTypeNo, move.toPath);
      const path = [...move.toPath];
      movesByNo.set(move.fromFieldNo, path);
      movesByName.set(move.fromName, { fromFieldNo: move.fromFieldNo, toPath: path });
    }
    if (rootType !== undefined && rootType.kind === 'object') {
      for (const name of movesByName.keys()) {
        if (rootType.fieldNoByName.has(name)) {
          throw new DecodeError(
            'MIGRATION_CONFLICT',
            `migrated name "${name}" still exists in current root object`,
          );
        }
      }
    }
    migrations.set(migration.fromVersion, { fromVersion: migration.fromVersion, movesByNo, movesByName });
  }

  const digest = createHash('sha256').update(canonicalGraphBytes(graph)).digest('hex');
  return {
    raw: graph,
    rootTypeNo: graph.rootTypeNo,
    version: graph.version,
    digest,
    types,
    migrations,
  };
}

function validateMigrationPath(
  types: Map<number, CompiledType>,
  rootTypeNo: number,
  toPath: number[],
): void {
  let current: CompiledType | undefined = types.get(rootTypeNo);
  for (let index = 0; index < toPath.length; index++) {
    if (current === undefined || current.kind !== 'object') {
      throw new DecodeError('SCHEMA_INVALID', 'migration path crosses non-object type');
    }
    const field = current.fieldsByNo.get(toPath[index]);
    if (field === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `migration path field ${toPath[index]} missing`);
    }
    current = types.get(field.typeNo);
  }
  if (current === undefined) {
    throw new DecodeError('SCHEMA_INVALID', 'migration path target type missing');
  }
}

function compileType(def: TypeDef, graph: TypeGraph): CompiledType {
  if (def.no === undefined || def.kind === undefined) {
    throw new DecodeError('SCHEMA_INVALID', 'type missing no/kind');
  }
  if (def.kind === 'object') {
    return compileObject(def);
  }
  if (def.kind === 'array') {
    if (typeof def.elementTypeNo !== 'number') {
      throw new DecodeError('SCHEMA_INVALID', `array ${def.no} missing elementTypeNo`);
    }
    return { kind: 'array', no: def.no, elementTypeNo: def.elementTypeNo };
  }
  if (def.kind === 'id64' || def.kind === 'int32' || def.kind === 'string' || def.kind === 'bool') {
    return { kind: def.kind, no: def.no };
  }
  throw new DecodeError('SCHEMA_INVALID', `unknown type kind in type ${def.no}`);
}

function compileObject(def: ObjectTypeDef): CompiledObject {
  const fieldsByNo = new Map<number, CompiledField>();
  const fieldNoByName = new Map<string, number>();
  const seenFieldNos = new Set<number>();

  for (const field of def.fields) {
    validateField(field);
    if (seenFieldNos.has(field.no)) {
      throw new DecodeError('SCHEMA_INVALID', `duplicate field no ${field.no} in type ${def.no}`);
    }
    seenFieldNos.add(field.no);
    for (const name of field.names) {
      const owner = fieldNoByName.get(name);
      if (owner !== undefined) {
        // 两个声明（两个字段编号）共用属性名：静态别名冲突，直接拒绝。
        throw new DecodeError(
          'ALIAS_COLLISION',
          `property name "${name}" bound to fields ${owner} and ${field.no}`,
        );
      }
      fieldNoByName.set(name, field.no);
    }
    fieldsByNo.set(field.no, {
      no: field.no,
      names: [...field.names],
      typeNo: field.typeNo,
      required: field.required === true,
    });
  }

  const sortedFields = [...fieldsByNo.values()].sort((a, b) => a.no - b.no);
  return { kind: 'object', no: def.no, fieldsByNo, fieldNoByName, sortedFields };
}

function validateField(field: FieldDef): void {
  if (typeof field.no !== 'number' || field.no < 0 || !Number.isInteger(field.no)) {
    throw new DecodeError('SCHEMA_INVALID', 'field no must be a non-negative integer');
  }
  if (!Array.isArray(field.names) || field.names.length === 0) {
    throw new DecodeError('SCHEMA_INVALID', `field ${field.no} needs at least one name`);
  }
  if (field.names.some((name) => typeof name !== 'string' || name.length === 0)) {
    throw new DecodeError('SCHEMA_INVALID', `field ${field.no} has empty name`);
  }
}

/**
 * 类型图的稳定字节摘要：所有对象按编号排序后以长度前缀拼接，
 * 使 digest 与对象字面量键顺序无关。检查点用它绑定 schema 版本。
 */
export function canonicalGraphBytes(graph: TypeGraph): Uint8Array {
  const parts: Uint8Array[] = [];
  pushByte(parts, 0x53); // 'S'
  pushU32(parts, graph.version);
  pushU32(parts, graph.rootTypeNo);

  const typeNos = Object.values(graph.types)
    .map((type) => type.no)
    .sort((a, b) => a - b);
  for (const no of typeNos) {
    const type = graph.types[no];
    pushU32(parts, type.no);
    pushBytes(parts, textEncoder.encode(type.kind));
    if (type.kind === 'object') {
      const fields = [...type.fields].sort((a, b) => a.no - b.no);
      pushU32(parts, fields.length);
      for (const field of fields) {
        pushU32(parts, field.no);
        pushU32(parts, field.typeNo);
        pushU32(parts, field.required === true ? 1 : 0);
        pushU32(parts, field.names.length);
        for (const name of field.names) {
          pushBytes(parts, textEncoder.encode(name));
        }
      }
    } else if (type.kind === 'array') {
      pushU32(parts, type.elementTypeNo);
    }
  }

  const migrations = [...(graph.migrations ?? [])].sort((a, b) => a.fromVersion - b.fromVersion);
  pushU32(parts, migrations.length);
  for (const migration of migrations) {
    pushU32(parts, migration.fromVersion);
    pushU32(parts, migration.moves.length);
    for (const move of migration.moves) {
      pushU32(parts, move.fromFieldNo);
      pushBytes(parts, textEncoder.encode(move.fromName));
      pushU32(parts, move.toPath.length);
      for (const segment of move.toPath) {
        pushU32(parts, segment);
      }
    }
  }
  return concat(parts);
}

function pushByte(parts: Uint8Array[], value: number): void {
  parts.push(Uint8Array.of(value));
}

function pushU32(parts: Uint8Array[], value: number): void {
  const buffer = new ArrayBuffer(4);
  new DataView(buffer).setUint32(0, value, false);
  parts.push(new Uint8Array(buffer));
}

function pushBytes(parts: Uint8Array[], bytes: Uint8Array): void {
  pushU32(parts, bytes.length);
  parts.push(bytes);
}

function concat(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}
