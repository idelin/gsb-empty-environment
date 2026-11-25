import { DecodeError, type DecodeOptions, type DecodeResult } from './types.ts';
import { id64FromBigInt } from './lex64.ts';
import type { CompiledGraph, CompiledMigration, CompiledType } from './schema.ts';
import {
  buildSignatureKey,
  encodeMinimal,
  makeArray,
  makeObject,
  sortIdentity,
  type IdNode,
} from './identity.ts';
import { revive, snapshotOf, type Snapshot } from './json.ts';

/**
 * 网络序二进制消息（所有整数大端）：
 *   message := u32 typeNo | u32 fieldCount | field*
 *   field   := u32 fieldNo | u32 length | value(length 字节)
 *   array   := u32 count | element*，element := u32 length | value
 * 值编码：bool 1 字节；int32 4 字节有符号；id64 8 字节有符号；
 * string 为 UTF-8 字节；object 的 value 是内嵌 message；
 * 数组元素（含标量）统一带 u32 长度前缀。
 * 未知非关键字段按 length 整体跳过，字节绝不进入授权身份。
 */

type ParseState =
  | 'messageType'
  | 'fieldCount'
  | 'fieldNo'
  | 'fieldLength'
  | 'valueBytes'
  | 'arrayCount'
  | 'elementLength'
  | 'skipBytes';

interface Frame {
  kind: 'object' | 'array';
  typeNo: number;
  node: IdNode;
  seen: number[];
  remaining: number;
  /** 容器值长度窗口（嵌套 message/array），根帧为 Infinity。 */
  valueWindow: number;
  pendingFieldNo: number;
  pendingLength: number;
  unknown: boolean;
  migrationPath: number[] | null;
  migrationFromNo: number;
  nodeFieldNo: number | null;
}

export interface BinaryCheckpoint {
  schemaDigest: string;
  inputVersion: number;
  state: ParseState;
  depth: number;
  need: number;
  accumulator: number;
  bytes: string;
  rootTypeNo: number | null;
  rootSnapshot: Snapshot | null;
  frames: Array<{
    kind: 'object' | 'array';
    typeNo: number;
    node: Snapshot;
    seen: number[];
    remaining: number;
    valueWindow: number;
    pendingFieldNo: number;
    pendingLength: number;
    unknown: boolean;
    migrationPath: number[] | null;
    migrationFromNo: number;
    nodeFieldNo: number | null;
  }>;
}

const I32_MIN = -2147483648;
const I32_MAX = 2147483647;

export class BinaryStreamDecoder {
  private readonly graph: CompiledGraph;
  private readonly options: DecodeOptions;
  private readonly migration: CompiledMigration | null;
  private state: ParseState = 'messageType';
  private need = 4;
  private accumulator = 0;
  private bytes: number[] = [];
  private readonly frames: Frame[] = [];
  private rootNode: IdNode | null = null;
  private rootTypeNo: number | null = null;
  private depth = 0;
  private finished = false;
  private errored = false;

  constructor(graph: CompiledGraph, options: DecodeOptions) {
    this.graph = graph;
    this.options = options;
    const inputVersion = options.inputVersion ?? graph.version;
    if (inputVersion === graph.version) {
      this.migration = null;
    } else {
      const found = graph.migrations.get(inputVersion);
      if (!found) {
        throw new DecodeError(
          'MIGRATION_UNKNOWN',
          `no migration from version ${inputVersion} to ${graph.version}`,
        );
      }
      this.migration = found;
    }
  }

  push(chunk: Uint8Array): void {
    if (this.finished) {
      throw new DecodeError('SYNTAX_ERROR', 'push after end of binary message');
    }
    try {
      for (const byte of chunk) {
        this.feed(byte);
      }
    } catch (error) {
      this.errored = true;
      throw error;
    }
  }

  end(): DecodeResult {
    if (this.errored) {
      throw new DecodeError('UNEXPECTED_END', 'decoder already failed; authorization never emitted');
    }
    if (this.finished) {
      throw new DecodeError('SYNTAX_ERROR', 'end called twice');
    }
    if (this.frames.length !== 0 || this.rootNode === null || this.rootTypeNo === null) {
      this.errored = true;
      throw new DecodeError('TRUNCATED_INPUT', 'incomplete binary message');
    }
    this.checkRequired(this.rootTypeNo, this.rootNode);
    sortIdentity(this.rootNode);
    const minimal = encodeMinimal(this.rootNode);
    this.finished = true;
    return {
      tenant: this.options.tenant,
      version: this.graph.version,
      rootTypeNo: this.rootTypeNo,
      schemaDigest: this.graph.digest,
      minimal,
      signatureKey: buildSignatureKey(this.graph, this.options.tenant, minimal),
      identity: this.rootNode,
    };
  }

  checkpoint(): BinaryCheckpoint {
    return {
      schemaDigest: this.graph.digest,
      inputVersion: this.options.inputVersion ?? this.graph.version,
      state: this.state,
      depth: this.depth,
      need: this.need,
      accumulator: this.accumulator,
      bytes: this.bytes.map((value) => String.fromCharCode(value)).join(''),
      rootTypeNo: this.rootTypeNo,
      rootSnapshot: this.rootNode === null ? null : snapshotOf(this.rootNode),
      frames: this.frames.map((frame) => ({
        kind: frame.kind,
        typeNo: frame.typeNo,
        node: snapshotOf(frame.node),
        seen: [...frame.seen],
        remaining: frame.remaining,
        valueWindow: frame.valueWindow,
        pendingFieldNo: frame.pendingFieldNo,
        pendingLength: frame.pendingLength,
        unknown: frame.unknown,
        migrationPath: frame.migrationPath === null ? null : [...frame.migrationPath],
        migrationFromNo: frame.migrationFromNo,
        nodeFieldNo: frame.nodeFieldNo,
      })),
    };
  }

  restore(checkpoint: BinaryCheckpoint): void {
    if (checkpoint.schemaDigest !== this.graph.digest) {
      throw new DecodeError('CHECKPOINT_DIGEST', 'checkpoint schema digest does not match graph');
    }
    const inputVersion = this.options.inputVersion ?? this.graph.version;
    if (checkpoint.inputVersion !== inputVersion) {
      throw new DecodeError('CHECKPOINT_STATE', 'checkpoint input version mismatch');
    }
    this.state = checkpoint.state;
    this.depth = checkpoint.depth;
    this.need = checkpoint.need;
    this.accumulator = checkpoint.accumulator;
    this.bytes = [...checkpoint.bytes].map((char) => char.charCodeAt(0));
    this.rootTypeNo = checkpoint.rootTypeNo;
    this.rootNode = checkpoint.rootSnapshot === null ? null : revive(checkpoint.rootSnapshot);
    this.frames.length = 0;
    for (let frameIndex = 0; frameIndex < checkpoint.frames.length; frameIndex++) {
      const cp = checkpoint.frames[frameIndex];
      let node: IdNode;
      if (frameIndex === 0) {
        if (this.rootNode === null) {
          throw new DecodeError('CHECKPOINT_STATE', 'root frame without root snapshot');
        }
        node = this.rootNode;
      } else {
        node = this.locateRestoredNode(frameIndex, checkpoint.frames);
      }
      this.frames.push({
        kind: cp.kind,
        typeNo: cp.typeNo,
        node,
        seen: [...cp.seen],
        remaining: cp.remaining,
        valueWindow: cp.valueWindow,
        pendingFieldNo: cp.pendingFieldNo,
        pendingLength: cp.pendingLength,
        unknown: cp.unknown,
        migrationPath: cp.migrationPath === null ? null : [...cp.migrationPath],
        migrationFromNo: cp.migrationFromNo,
        nodeFieldNo: cp.nodeFieldNo,
      });
    }
    this.finished = false;
    this.errored = false;
  }

  private locateRestoredNode(
    frameIndex: number,
    frameCheckpoints: BinaryCheckpoint['frames'],
  ): IdNode {
    const frameCp = frameCheckpoints[frameIndex];
    let current = this.rootNode as IdNode;
    for (let index = 1; index <= frameIndex; index++) {
      const step = frameCheckpoints[index];
      if (current.kind === 'array') {
        if (step.kind !== 'object' || current.elements.length === 0) {
          throw new DecodeError('CHECKPOINT_STATE', 'restored array path has no object element');
        }
        current = current.elements[current.elements.length - 1];
      } else if (current.kind === 'object') {
        const fieldNo = step.nodeFieldNo;
        if (fieldNo === null) {
          throw new DecodeError('CHECKPOINT_STATE', 'restored object path lacks field anchor');
        }
        const slot = current.fields.find((field) => field.no === fieldNo);
        if (slot === undefined) {
          throw new DecodeError('CHECKPOINT_STATE', `restored field ${fieldNo} not found in tree`);
        }
        current = slot.value;
      }
      if (current.typeNo !== step.typeNo || current.kind !== step.kind) {
        throw new DecodeError('CHECKPOINT_STATE', 'restored frame node type mismatch');
      }
    }
    if (current.typeNo !== frameCp.typeNo || current.kind !== frameCp.kind) {
      throw new DecodeError('CHECKPOINT_STATE', 'restored node type mismatch');
    }
    return current;
  }

  private topFrame(): Frame {
    const frame = this.frames[this.frames.length - 1];
    if (frame === undefined) {
      throw new DecodeError('SYNTAX_ERROR', 'binary parser frame underflow');
    }
    return frame;
  }

  private makeFrame(
    kind: 'object' | 'array',
    typeNo: number,
    node: IdNode,
    valueWindow: number,
    nodeFieldNo: number | null = null,
  ): Frame {
    return {
      kind,
      typeNo,
      node,
      seen: [],
      remaining: 0,
      valueWindow,
      pendingFieldNo: -1,
      pendingLength: 0,
      unknown: false,
      migrationPath: null,
      migrationFromNo: -1,
      nodeFieldNo,
    };
  }

  private feed(byte: number): void {
    switch (this.state) {
      case 'messageType':
      case 'fieldCount':
      case 'fieldNo':
      case 'fieldLength':
      case 'arrayCount':
      case 'elementLength':
        this.feedU32(byte);
        return;
      case 'valueBytes':
      case 'skipBytes':
        this.feedBuffered(byte);
        return;
    }
  }

  private feedU32(byte: number): void {
    this.accumulator = ((this.accumulator << 8) | (byte & 0xff)) >>> 0;
    this.need -= 1;
    this.decrementWindows();
    if (this.need !== 0) {
      return;
    }
    const value = this.accumulator;
    this.accumulator = 0;
    switch (this.state) {
      case 'messageType':
        this.beginMessage(value);
        return;
      case 'fieldCount':
        this.beginFieldCount(value);
        return;
      case 'fieldNo':
        this.beginFieldNo(value);
        return;
      case 'fieldLength':
        this.beginFieldLength(value);
        return;
      case 'arrayCount':
        this.beginArrayCount(value);
        return;
      case 'elementLength':
        this.beginElementLength(value);
        return;
      default:
        throw new DecodeError('SYNTAX_ERROR', `u32 complete in bad state ${this.state}`);
    }
  }

  private beginMessage(typeNo: number): void {
    const type = this.graph.types.get(typeNo);
    if (type === undefined) {
      throw new DecodeError('TYPE_MISMATCH', `unknown message type ${typeNo}`);
    }
    if (this.rootNode === null) {
      if (typeNo !== this.graph.rootTypeNo) {
        throw new DecodeError('TYPE_MISMATCH', `root message type ${typeNo} != ${this.graph.rootTypeNo}`);
      }
      if (type.kind !== 'object') {
        throw new DecodeError('TYPE_MISMATCH', 'root message must encode an object');
      }
      this.rootTypeNo = typeNo;
      this.rootNode = makeObject(typeNo);
      this.frames.push(this.makeFrame('object', typeNo, this.rootNode, Number.POSITIVE_INFINITY));
      this.depth = 1;
    } else {
      const frame = this.topFrame();
      if (frame.typeNo !== typeNo || frame.kind !== 'object') {
        throw new DecodeError(
          'TYPE_MISMATCH',
          `nested message type ${typeNo} != declared ${frame.typeNo}`,
        );
      }
    }
    this.state = 'fieldCount';
    this.need = 4;
    this.accumulator = 0;
  }

  private beginFieldCount(count: number): void {
    const frame = this.topFrame();
    if (frame.kind !== 'object') {
      throw new DecodeError('TYPE_MISMATCH', 'field count on non-object frame');
    }
    frame.remaining = count;
    if (count === 0) {
      this.closeFrame(frame);
    } else {
      this.state = 'fieldNo';
      this.need = 4;
      this.accumulator = 0;
    }
  }

  private beginFieldNo(fieldNo: number): void {
    const frame = this.topFrame();
    const objectType = this.requireObjectType(frame.typeNo);
    const field = objectType.fieldsByNo.get(fieldNo);
    frame.pendingFieldNo = fieldNo;
    frame.unknown = false;
    frame.migrationPath = null;
    frame.migrationFromNo = -1;

    if (field !== undefined) {
      this.state = 'fieldLength';
      this.need = 4;
      this.accumulator = 0;
      return;
    }
    if (this.migration !== null && this.frames.length === 1) {
      const path = this.migration.movesByNo.get(fieldNo);
      if (path !== undefined) {
        frame.migrationPath = path;
        frame.migrationFromNo = fieldNo;
        this.state = 'fieldLength';
        this.need = 4;
        this.accumulator = 0;
        return;
      }
    }
    if (this.options.strictUnknown === true) {
      throw new DecodeError('UNKNOWN_FIELD', `unknown field no ${fieldNo} in type ${frame.typeNo}`);
    }
    frame.unknown = true;
    this.state = 'fieldLength';
    this.need = 4;
    this.accumulator = 0;
  }

  private beginFieldLength(length: number): void {
    const frame = this.topFrame();
    frame.pendingLength = length;
    if (frame.unknown) {
      this.bytes = [];
      this.need = length;
      this.state = 'skipBytes';
      if (length === 0) {
        this.finishBuffered();
      }
      return;
    }
    const valueTypeNo =
      frame.migrationPath !== null
        ? this.typeAtPath(this.graph.rootTypeNo, frame.migrationPath)
        : this.requireObjectType(frame.typeNo).fieldsByNo.get(frame.pendingFieldNo)?.typeNo;
    if (valueTypeNo === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `field ${frame.pendingFieldNo} missing`);
    }
    const valueType = this.graph.types.get(valueTypeNo);
    if (valueType?.kind === 'object') {
      // 对象 value 是内嵌 message：先压帧，再读它的 u32 typeNo。
      const node = makeObject(valueTypeNo);
      this.attachContainerNode(frame, node, frame.pendingFieldNo);
      const child = this.makeFrame('object', valueTypeNo, node, length, frame.pendingFieldNo);
      this.frames.push(child);
      this.depth += 1;
      this.state = 'messageType';
      this.need = 4;
      this.accumulator = 0;
      return;
    }
    if (valueType?.kind === 'array') {
      const node = makeArray(valueTypeNo);
      if (frame.node.kind !== 'object') {
        throw new DecodeError('SCHEMA_INVALID', 'object frame holds non-object node');
      }
      if (frame.seen.includes(frame.pendingFieldNo)) {
        throw new DecodeError('DUPLICATE_FIELD', `field no ${frame.pendingFieldNo} repeated`);
      }
      frame.seen.push(frame.pendingFieldNo);
      frame.node.fields.push({ no: frame.pendingFieldNo, value: node });
      const child = this.makeFrame('array', valueTypeNo, node, Number.POSITIVE_INFINITY, frame.pendingFieldNo);
      this.frames.push(child);
      this.depth += 1;
      this.state = 'arrayCount';
      this.need = 4;
      this.accumulator = 0;
      return;
    }
    this.bytes = [];
    this.need = length;
    this.state = 'valueBytes';
    if (length === 0) {
      this.finishBuffered();
    }
  }

  private beginArrayCount(count: number): void {
    const frame = this.topFrame();
    if (frame.kind !== 'array') {
      throw new DecodeError('TYPE_MISMATCH', 'array count on non-array frame');
    }
    frame.remaining = count;
    if (count === 0) {
      this.closeFrame(frame);
    } else {
      this.state = 'elementLength';
      this.need = 4;
      this.accumulator = 0;
    }
  }

  private beginElementLength(length: number): void {
    const frame = this.topFrame();
    frame.pendingLength = length;
    const elementTypeNo = this.requireArrayType(frame.typeNo).elementTypeNo;
    const elementType = this.graph.types.get(elementTypeNo);
    if (elementType?.kind === 'object') {
      const node = makeObject(elementTypeNo);
      if (frame.node.kind !== 'array') {
        throw new DecodeError('SCHEMA_INVALID', 'array frame holds non-array node');
      }
      frame.node.elements.push(node);
      const child = this.makeFrame('object', elementTypeNo, node, length);
      this.frames.push(child);
      this.depth += 1;
      this.state = 'messageType';
      this.need = 4;
      this.accumulator = 0;
      return;
    }
    if (elementType?.kind === 'array') {
      const node = makeArray(elementTypeNo);
      if (frame.node.kind !== 'array') {
        throw new DecodeError('SCHEMA_INVALID', 'array frame holds non-array node');
      }
      frame.node.elements.push(node);
      const child = this.makeFrame('array', elementTypeNo, node, Number.POSITIVE_INFINITY);
      this.frames.push(child);
      this.depth += 1;
      this.state = 'arrayCount';
      this.need = 4;
      this.accumulator = 0;
      return;
    }
    this.bytes = [];
    this.need = length;
    this.state = 'valueBytes';
    if (length === 0) {
      this.finishBuffered();
    }
  }

  private feedBuffered(byte: number): void {
    this.bytes.push(byte);
    this.need -= 1;
    this.decrementWindows();
    if (this.need !== 0) {
      return;
    }
    this.finishBuffered();
  }

  /** 每个字节递减栈上全部对象帧窗口；数组/根帧为 Infinity 自动跳过。 */
  private decrementWindows(): void {
    for (const frame of this.frames) {
      if (frame.valueWindow === Number.POSITIVE_INFINITY) {
        continue;
      }
      frame.valueWindow -= 1;
      if (frame.valueWindow < 0) {
        throw new DecodeError('SYNTAX_ERROR', 'nested value exceeds declared length window');
      }
    }
  }

  private finishBuffered(): void {
    const frame = this.topFrame();
    if (this.state === 'skipBytes') {
      // 未知字段：length 字节全部丢弃，不解析也不进入身份。
      this.advanceObjectField(frame);
      return;
    }
    if (frame.kind === 'array') {
      const elementTypeNo = this.requireArrayType(frame.typeNo).elementTypeNo;
      const node = this.decodeScalarOrOpen(elementTypeNo, this.bytes);
      if (frame.node.kind === 'array') {
        frame.node.elements.push(node);
      }
      frame.remaining -= 1;
      if (frame.remaining > 0) {
        this.state = 'elementLength';
        this.need = 4;
        this.accumulator = 0;
      } else {
        this.closeFrame(frame);
      }
      return;
    }

    if (frame.migrationPath !== null) {
      const valueTypeNo = this.typeAtPath(this.graph.rootTypeNo, frame.migrationPath);
      const node = this.decodeScalarOrOpen(valueTypeNo, this.bytes);
      if (frame.seen.includes(frame.migrationFromNo)) {
        throw new DecodeError('DUPLICATE_FIELD', `migrated field no ${frame.migrationFromNo} repeated`);
      }
      frame.seen.push(frame.migrationFromNo);
      this.placeMigration(this.rootNode as IdNode, frame.migrationPath, frame.migrationFromNo, node);
      this.advanceObjectField(frame);
      return;
    }

    const objectType = this.requireObjectType(frame.typeNo);
    const field = objectType.fieldsByNo.get(frame.pendingFieldNo);
    if (field === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `field ${frame.pendingFieldNo} missing`);
    }
    const node = this.decodeScalarOrOpen(field.typeNo, this.bytes);
    if (frame.seen.includes(frame.pendingFieldNo)) {
      throw new DecodeError('DUPLICATE_FIELD', `field no ${frame.pendingFieldNo} repeated`);
    }
    frame.seen.push(frame.pendingFieldNo);
    if (frame.node.kind !== 'object') {
      throw new DecodeError('SCHEMA_INVALID', 'object frame holds non-object node');
    }
    frame.node.fields.push({ no: frame.pendingFieldNo, value: node });
    this.advanceObjectField(frame);
  }

  private advanceObjectField(frame: Frame): void {
    frame.remaining -= 1;
    if (frame.remaining > 0) {
      this.state = 'fieldNo';
      this.need = 4;
      this.accumulator = 0;
    } else {
      this.closeFrame(frame);
    }
  }

  /**
   * 解码一个 length 窗口里的值。对象/数组不在此处解码，而是切换状态机
   * 继续流式解析并返回 null；窗口耗尽时由 closeFrame 回填父节点。
   */
  private decodeScalarOrOpen(typeNo: number, bytes: number[]): IdNode {
    const type = this.graph.types.get(typeNo);
    if (type === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `value type ${typeNo} missing`);
    }
    if (type.kind === 'object' || type.kind === 'array') {
      throw new DecodeError('SYNTAX_ERROR', `container type ${typeNo} reached scalar buffer path`);
    }

    const data = Uint8Array.from(bytes);
    switch (type.kind) {
      case 'bool':
        if (data.length !== 1 || (data[0] !== 0 && data[0] !== 1)) {
          throw new DecodeError('RANGE_ERROR', `bool must be one byte 0/1, got ${data.length}`);
        }
        return { kind: 'bool', typeNo, value: data[0] === 1 };
      case 'int32': {
        if (data.length !== 4) {
          throw new DecodeError('RANGE_ERROR', `int32 needs 4 bytes, got ${data.length}`);
        }
        const value = new DataView(
          data.buffer.slice(data.byteOffset, data.byteOffset + 4),
        ).getInt32(0, false);
        if (value < I32_MIN || value > I32_MAX) {
          throw new DecodeError('RANGE_ERROR', `int32 out of range: ${value}`);
        }
        return { kind: 'int32', typeNo, value };
      }
      case 'id64': {
        if (data.length !== 8) {
          throw new DecodeError('RANGE_ERROR', `id64 needs 8 bytes, got ${data.length}`);
        }
        const buffer = data.buffer.slice(data.byteOffset, data.byteOffset + 8);
        const view = new DataView(buffer);
        const negative = (view.getUint8(0) & 0x80) !== 0;
        let value = view.getBigUint64(0, false);
        if (negative) {
          value -= 1n << 64n;
        }
        return { kind: 'id64', typeNo, value: id64FromBigInt(value) };
      }
      case 'string':
        return { kind: 'string', typeNo, value: decodeUtf8Strict(data) };
    }
  }

  /** 容器开帧即挂树，保证检查点根树始终包含未闭合容器。 */
  private attachContainerNode(parent: Frame, node: IdNode, fieldNo: number): void {
    if (parent.kind !== 'object' || parent.node.kind !== 'object') {
      throw new DecodeError('SCHEMA_INVALID', 'container field parent is not an object');
    }
    if (parent.seen.includes(fieldNo)) {
      throw new DecodeError('DUPLICATE_FIELD', `field no ${fieldNo} repeated`);
    }
    parent.seen.push(fieldNo);
    parent.node.fields.push({ no: fieldNo, value: node });
  }

  private closeFrame(closing: Frame): void {
    if (closing.valueWindow !== Number.POSITIVE_INFINITY && closing.valueWindow !== 0) {
      throw new DecodeError('SYNTAX_ERROR', 'nested container did not consume its full length window');
    }
    this.frames.pop();
    this.depth -= 1;
    const parent = this.frames[this.frames.length - 1];
    if (parent === undefined) {
      // 根对象闭合：回到初始状态，end() 校验无残留。
      this.state = 'messageType';
      this.need = 4;
      this.accumulator = 0;
      this.bytes = [];
      return;
    }

    if (parent.kind === 'array') {
      if (parent.node.kind !== 'array') {
        throw new DecodeError('SCHEMA_INVALID', 'array parent holds non-array node');
      }
      // 元素节点在 elementLength 开帧时已入树，这里只推进计数。
      parent.remaining -= 1;
      if (parent.remaining > 0) {
        this.state = 'elementLength';
        this.need = 4;
        this.accumulator = 0;
      } else {
        this.closeFrame(parent);
      }
      return;
    }

    if (parent.unknown) {
      // 嵌套未知容器整体在 length 窗口内被消费，但不进入身份。
      this.advanceObjectField(parent);
      return;
    }
    if (parent.migrationPath !== null) {
      if (parent.seen.includes(parent.migrationFromNo)) {
        throw new DecodeError('DUPLICATE_FIELD', `migrated field no ${parent.migrationFromNo} repeated`);
      }
      parent.seen.push(parent.migrationFromNo);
      this.placeMigration(
        this.rootNode as IdNode,
        parent.migrationPath,
        parent.migrationFromNo,
        closing.node,
      );
      this.advanceObjectField(parent);
      return;
    }
    // 容器节点在 beginFieldLength 开帧时已挂树并记入 seen。
    this.advanceObjectField(parent);
  }

  private placeMigration(
    root: IdNode,
    path: number[],
    fromFieldNo: number,
    value: IdNode,
  ): void {
    let current = root;
    for (let index = 0; index < path.length - 1; index++) {
      if (current.kind !== 'object') {
        throw new DecodeError('MIGRATION_CONFLICT', 'migration path crosses non-object identity');
      }
      const segment = path[index];
      let next = current.fields.find((field) => field.no === segment);
      if (next === undefined) {
        const targetTypeNo = this.typeAtPath(this.graph.rootTypeNo, path.slice(0, index + 1));
        next = { no: segment, value: makeObject(targetTypeNo) };
        current.fields.push(next);
      }
      current = next.value;
    }
    if (current.kind !== 'object') {
      throw new DecodeError('MIGRATION_CONFLICT', 'migration destination is not an object');
    }
    const destinationNo = path[path.length - 1];
    if (current.fields.some((field) => field.no === destinationNo)) {
      throw new DecodeError(
        'MIGRATION_CONFLICT',
        `migration destination field ${destinationNo} already populated`,
      );
    }
    current.fields.push({ no: destinationNo, value });
  }

  private typeAtPath(rootTypeNo: number, path: number[]): number {
    let current = this.requireObjectType(rootTypeNo);
    let typeNo = rootTypeNo;
    for (const segment of path) {
      const field = current.fieldsByNo.get(segment);
      if (field === undefined) {
        throw new DecodeError('SCHEMA_INVALID', `migration path field ${segment} missing`);
      }
      typeNo = field.typeNo;
      const next = this.graph.types.get(typeNo);
      if (next === undefined) {
        throw new DecodeError('SCHEMA_INVALID', `migration path type ${typeNo} missing`);
      }
      if (next.kind === 'object') {
        current = next;
      }
    }
    return typeNo;
  }

  private requireObjectType(typeNo: number): Extract<CompiledType, { kind: 'object' }> {
    const type = this.graph.types.get(typeNo);
    if (type === undefined || type.kind !== 'object') {
      throw new DecodeError('TYPE_MISMATCH', `type ${typeNo} is not an object`);
    }
    return type;
  }

  private requireArrayType(typeNo: number): Extract<CompiledType, { kind: 'array' }> {
    const type = this.graph.types.get(typeNo);
    if (type === undefined || type.kind !== 'array') {
      throw new DecodeError('TYPE_MISMATCH', `type ${typeNo} is not an array`);
    }
    return type;
  }

  private checkRequired(typeNo: number, node: IdNode): void {
    const type = this.graph.types.get(typeNo);
    if (type === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `type ${typeNo} missing in required check`);
    }
    if (type.kind === 'object' && node.kind === 'object') {
      const present = new Set(node.fields.map((field) => field.no));
      for (const field of type.sortedFields) {
        if (field.required && !present.has(field.no)) {
          throw new DecodeError('TYPE_MISMATCH', `required field ${field.no} missing in type ${typeNo}`);
        }
      }
      for (const field of node.fields) {
        this.checkRequired(field.value.typeNo, field.value);
      }
    } else if (type.kind === 'array' && node.kind === 'array') {
      for (const element of node.elements) {
        this.checkRequired(element.typeNo, element);
      }
    }
  }
}

function decodeUtf8Strict(data: Uint8Array): string {
  let result = '';
  let index = 0;
  while (index < data.length) {
    const first = data[index];
    let codePoint: number;
    let step: number;
    if (first < 0x80) {
      codePoint = first;
      step = 1;
    } else if (first >= 0xc0 && first < 0xe0) {
      codePoint = first & 0x1f;
      step = 2;
    } else if (first >= 0xe0 && first < 0xf0) {
      codePoint = first & 0x0f;
      step = 3;
    } else if (first >= 0xf0 && first < 0xf8) {
      codePoint = first & 0x07;
      step = 4;
    } else {
      throw new DecodeError('SYNTAX_ERROR', 'invalid UTF-8 lead byte in string value');
    }
    if (index + step > data.length) {
      throw new DecodeError('TRUNCATED_INPUT', 'incomplete UTF-8 in string value');
    }
    for (let tail = 1; tail < step; tail++) {
      const byte = data[index + tail];
      if ((byte & 0xc0) !== 0x80) {
        throw new DecodeError('SYNTAX_ERROR', 'invalid UTF-8 continuation byte');
      }
      codePoint = (codePoint << 6) | (byte & 0x3f);
    }
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) {
      throw new DecodeError('LONE_SURROGATE', `surrogate code point U+${codePoint.toString(16)}`);
    }
    if (codePoint > 0x10ffff) {
      throw new DecodeError('SYNTAX_ERROR', 'UTF-8 code point beyond U+10FFFF');
    }
    result += String.fromCodePoint(codePoint);
    index += step;
  }
  return result;
}
