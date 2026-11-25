import {
  DecodeError,
  type DecodeOptions,
  type DecodeResult,
} from './types.ts';
import {
  canonicalId64FromNumberLexeme,
  canonicalId64FromText,
} from './lex64.ts';
import { JsonLexer, type LexerCheckpoint, type Token } from './lexer.ts';
import type { CompiledGraph, CompiledMigration, CompiledType } from './schema.ts';
import {
  buildSignatureKey,
  encodeMinimal,
  makeArray,
  makeObject,
  sortIdentity,
  type IdNode,
} from './identity.ts';

const I32_MIN = -2147483648;
const I32_MAX = 2147483647;

type Expect = 'value' | 'colon' | 'commaOrEnd' | 'done';

interface PendingField {
  fieldNo: number;
  typeNo: number;
  unknown: boolean;
  migrationPath: number[] | null;
}

interface CaptureFrame {
  kind: 'object' | 'array';
  typeNo: number;
  seen: number[];
  pendingFieldNo: number | null;
  scratchArray: Snapshot[];
  scratchFields: Array<{ no: number; value: Snapshot }>;
}

interface Capture {
  path: number[];
  fromFieldNo: number;
  frames: CaptureFrame[];
  rootSnapshot: Snapshot | null;
  depth: number;
}

interface Frame {
  kind: 'object' | 'array';
  typeNo: number;
  node: IdNode;
  seen: number[];
  expect: Expect;
  pendingKey: string | null;
  pendingField: PendingField | null;
  capture: Capture | null;
  nodeFieldNo: number | null;
}

export type Snapshot =
  | { kind: 'object'; typeNo: number; fields: Array<{ no: number; value: Snapshot }> }
  | { kind: 'array'; typeNo: number; elements: Snapshot[] }
  | { kind: 'id64'; typeNo: number; value: string }
  | { kind: 'int32'; typeNo: number; value: number }
  | { kind: 'string'; typeNo: number; value: string }
  | { kind: 'bool'; typeNo: number; value: boolean };

interface CaptureCheckpoint {
  path: number[];
  fromFieldNo: number;
  frames: CaptureFrame[];
  rootSnapshot: Snapshot | null;
  depth: number;
}

export interface JsonCheckpoint {
  schemaDigest: string;
  inputVersion: number;
  depth: number;
  expect: Expect | null;
  lexer: LexerCheckpoint;
  rootSnapshot: Snapshot | null;
  frames: Array<{
    kind: 'object' | 'array';
    typeNo: number;
    node: Snapshot;
    seen: number[];
    expect: Expect;
    pendingKey: string | null;
    pendingField: PendingField | null;
    capture: CaptureCheckpoint | null;
    nodeFieldNo: number | null;
  }>;
}

export class JsonStreamDecoder {
  private readonly graph: CompiledGraph;
  private readonly options: DecodeOptions;
  private readonly migration: CompiledMigration | null;
  private readonly lexer = new JsonLexer();
  private readonly frames: Frame[] = [];
  private rootNode: IdNode | null = null;
  private expect: Expect | null = 'value';
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
      throw new DecodeError('SYNTAX_ERROR', 'push after end of JSON message');
    }
    let tokens: Token[];
    try {
      tokens = this.lexer.push(chunk);
    } catch (error) {
      this.errored = true;
      throw error;
    }
    for (const token of tokens) {
      try {
        this.consume(token);
      } catch (error) {
        this.errored = true;
        throw error;
      }
    }
  }

  end(): DecodeResult {
    if (this.errored) {
      throw new DecodeError('UNEXPECTED_END', 'decoder already failed; authorization never emitted');
    }
    if (this.finished) {
      throw new DecodeError('SYNTAX_ERROR', 'end called twice');
    }
    try {
      for (const token of this.lexer.finish()) {
        this.consume(token);
      }
    } catch (error) {
      this.errored = true;
      throw error;
    }
    if (this.frames.length !== 0 || this.rootNode === null || this.expect !== 'done') {
      this.errored = true;
      throw new DecodeError('TRUNCATED_INPUT', 'unclosed JSON structure at end of input');
    }
    this.checkRequired(this.graph.rootTypeNo, this.rootNode);
    sortIdentity(this.rootNode);
    const minimal = encodeMinimal(this.rootNode);
    this.finished = true;
    return {
      tenant: this.options.tenant,
      version: this.graph.version,
      rootTypeNo: this.graph.rootTypeNo,
      schemaDigest: this.graph.digest,
      minimal,
      signatureKey: buildSignatureKey(this.graph, this.options.tenant, minimal),
      identity: this.rootNode,
    };
  }

  checkpoint(): JsonCheckpoint {
    return {
      schemaDigest: this.graph.digest,
      inputVersion: this.options.inputVersion ?? this.graph.version,
      depth: this.depth,
      expect: this.expect,
      lexer: this.lexer.checkpoint(),
      rootSnapshot: this.rootNode === null ? null : snapshotOf(this.rootNode),
      frames: this.frames.map((frame) => ({
        kind: frame.kind,
        typeNo: frame.typeNo,
        node: snapshotOf(frame.node),
        seen: [...frame.seen],
        expect: frame.expect,
        pendingKey: frame.pendingKey,
        pendingField: frame.pendingField === null ? null : { ...frame.pendingField },
        capture: frame.capture === null ? null : captureCheckpoint(frame.capture),
        nodeFieldNo: frame.nodeFieldNo,
      })),
    };
  }

  restore(checkpoint: JsonCheckpoint): void {
    if (checkpoint.schemaDigest !== this.graph.digest) {
      throw new DecodeError('CHECKPOINT_DIGEST', 'checkpoint schema digest does not match graph');
    }
    const inputVersion = this.options.inputVersion ?? this.graph.version;
    if (checkpoint.inputVersion !== inputVersion) {
      throw new DecodeError('CHECKPOINT_STATE', 'checkpoint input version mismatch');
    }
    this.lexer.restore(checkpoint.lexer);
    this.depth = checkpoint.depth;
    this.expect = checkpoint.expect;
    this.rootNode = checkpoint.rootSnapshot === null ? null : revive(checkpoint.rootSnapshot);
    this.frames.length = 0;
    for (let frameIndex = 0; frameIndex < checkpoint.frames.length; frameIndex++) {
      const frameCp = checkpoint.frames[frameIndex];
      let node: IdNode;
      if (frameIndex === 0) {
        if (this.rootNode === null) {
          throw new DecodeError('CHECKPOINT_STATE', 'root frame without root snapshot');
        }
        node = this.rootNode;
      } else {
        node = this.locateRestoredNode(frameIndex, checkpoint.frames);
      }
      const frame: Frame = {
        kind: frameCp.kind,
        typeNo: frameCp.typeNo,
        node,
        seen: [...frameCp.seen],
        expect: frameCp.expect,
        pendingKey: frameCp.pendingKey,
        pendingField: frameCp.pendingField === null ? null : { ...frameCp.pendingField },
        capture: frameCp.capture === null ? null : restoreCapture(frameCp.capture),
        nodeFieldNo: frameCp.nodeFieldNo,
      };
      this.frames.push(frame);
    }
    this.finished = false;
    this.errored = false;
  }

  /**
   * 恢复时所有容器节点都已在根树中（开帧即入树）。
   * 从根节点出发，沿帧链逐级下降：对象帧按 nodeFieldNo 取字段，
   * 数组帧取最后一个元素，保证全部帧共享同一棵可变身份树。
   */
  private locateRestoredNode(
    frameIndex: number,
    frameCheckpoints: JsonCheckpoint['frames'],
  ): IdNode {
    const frameCp = frameCheckpoints[frameIndex];
    let current = this.rootNode as IdNode;
    for (let index = 1; index <= frameIndex; index++) {
      const step = frameCheckpoints[index];
      if (step.typeNo === -1) {
        return step.kind === 'object' ? makeObject(-1) : makeArray(-1);
      }
      if (current.kind === 'array') {
        // 父节点是数组：当前帧必然是其末尾元素对象。
        if (step.kind !== 'object' || current.elements.length === 0) {
          throw new DecodeError('CHECKPOINT_STATE', 'restored array path has no object element');
        }
        current = current.elements[current.elements.length - 1];
      } else if (current.kind === 'object') {
        // 父节点是对象：按当前帧的字段编号取字段值。
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

  /* ---------------------------- 语法驱动 ---------------------------- */

  private consume(token: Token): void {
    const frame = this.frames[this.frames.length - 1];
    if (frame === undefined) {
      this.consumeRoot(token);
      return;
    }
    if (frame.typeNo === -1) {
      this.consumeSkippedFrame(frame, token);
      return;
    }
    if (frame.kind === 'array') {
      this.consumeArray(frame, token);
    } else {
      this.consumeObject(frame, token);
    }
  }

  /** 未知字段值的纯语法跳过：任何合法 JSON 结构都被吞掉，不进入身份。 */
  private consumeSkippedFrame(frame: Frame, token: Token): void {
    if (frame.kind === 'array') {
      if (frame.expect === 'commaOrEnd') {
        if (token.type === 'endArray') {
          this.closeFrame();
          return;
        }
        if (token.type !== 'comma') {
          throw new DecodeError('SYNTAX_ERROR', 'expected comma or end of skipped array');
        }
        frame.expect = 'value';
        return;
      }
      if (token.type === 'endArray') {
        this.closeFrame();
        return;
      }
      this.consumeSkippedValue(frame, token);
      return;
    }

    if (frame.expect === 'value') {
      if (token.type === 'endObject' && frame.pendingKey === null) {
        this.closeFrame();
        return;
      }
      if (frame.pendingKey === null) {
        if (token.type !== 'string') {
          throw new DecodeError('SYNTAX_ERROR', 'skipped object key must be a string');
        }
        frame.pendingKey = token.value;
        frame.expect = 'colon';
        return;
      }
      this.consumeSkippedValue(frame, token);
      return;
    }
    if (frame.expect === 'colon') {
      if (token.type !== 'colon') {
        throw new DecodeError('SYNTAX_ERROR', 'expected colon in skipped object');
      }
      frame.expect = 'value';
      return;
    }
    if (token.type === 'endObject') {
      this.closeFrame();
      return;
    }
    if (token.type !== 'comma') {
      throw new DecodeError('SYNTAX_ERROR', 'expected comma or end of skipped object');
    }
    frame.pendingKey = null;
    frame.expect = 'value';
  }

  private consumeSkippedValue(frame: Frame, token: Token): void {
    if (token.type === 'beginObject' || token.type === 'beginArray') {
      this.frames.push({
        kind: token.type === 'beginObject' ? 'object' : 'array',
        typeNo: -1,
        node: token.type === 'beginObject' ? makeObject(-1) : makeArray(-1),
        seen: [],
        expect: 'value',
        pendingKey: null,
        pendingField: null,
        capture: null,
        nodeFieldNo: null,
      });
      this.depth += 1;
      return;
    }
    if (!this.isScalarStart(token)) {
      throw new DecodeError('SYNTAX_ERROR', `bad skipped value token ${token.type}`);
    }
    frame.expect = 'commaOrEnd';
    frame.pendingKey = null;
  }

  private consumeRoot(token: Token): void {
    if (this.expect === 'done') {
      throw new DecodeError('SYNTAX_ERROR', 'unexpected token after root value');
    }
    if (token.type === 'beginObject' || token.type === 'beginArray') {
      const node =
        token.type === 'beginObject'
          ? makeObject(this.graph.rootTypeNo)
          : makeArray(this.graph.rootTypeNo);
      this.rootNode = node;
      this.frames.push({
        kind: token.type === 'beginObject' ? 'object' : 'array',
        typeNo: this.graph.rootTypeNo,
        node,
        seen: [],
        expect: 'value',
        pendingKey: null,
        pendingField: null,
        capture: null,
        nodeFieldNo: null,
      });
      this.depth = 1;
      return;
    }
    if (this.isScalarStart(token)) {
      this.rootNode = this.scalarNode(this.graph.rootTypeNo, token);
      this.expect = 'done';
      return;
    }
    throw new DecodeError('SYNTAX_ERROR', `root must be a value, got ${token.type}`);
  }

  private consumeObject(frame: Frame, token: Token): void {
    if (frame.expect === 'value') {
      if (token.type === 'endObject' && frame.pendingKey === null) {
        this.closeFrame();
        return;
      }
      // 冒号已消费、pendingField 已解析：当前 token 是字段值。
      if (frame.pendingField !== null || frame.capture !== null) {
        this.readValueToken(frame, token);
        return;
      }
      if (token.type !== 'string') {
        throw new DecodeError('SYNTAX_ERROR', 'object key must be a string');
      }
      frame.pendingKey = token.value;
      frame.expect = 'colon';
      return;
    }
    if (frame.expect === 'colon') {
      if (token.type !== 'colon') {
        throw new DecodeError('SYNTAX_ERROR', 'expected colon after object key');
      }
      this.resolvePendingField(frame);
      frame.expect = 'value';
      return;
    }
    if (frame.expect === 'commaOrEnd') {
      if (token.type === 'endObject') {
        this.closeFrame();
        return;
      }
      if (token.type !== 'comma') {
        throw new DecodeError('SYNTAX_ERROR', 'expected comma or end of object');
      }
      frame.expect = 'value';
      return;
    }
    throw new DecodeError('SYNTAX_ERROR', `unexpected object token ${token.type}`);
  }

  private consumeArray(frame: Frame, token: Token): void {
    if (frame.expect === 'commaOrEnd') {
      if (token.type === 'endArray') {
        this.closeFrame();
        return;
      }
      if (token.type !== 'comma') {
        throw new DecodeError('SYNTAX_ERROR', 'expected comma or end of array');
      }
      frame.expect = 'value';
      return;
    }
    if (
      token.type === 'endArray' &&
      frame.node.kind === 'array' &&
      frame.node.elements.length === 0 &&
      frame.capture === null
    ) {
      this.closeFrame();
      return;
    }
    this.readValueToken(frame, token);
  }

  private resolvePendingField(frame: Frame): void {
    const key = frame.pendingKey as string;
    const objectType = this.requireObjectType(frame.typeNo);
    const fieldNo = objectType.fieldNoByName.get(key);

    if (fieldNo !== undefined) {
      const field = objectType.fieldsByNo.get(fieldNo);
      if (field === undefined) {
        throw new DecodeError('SCHEMA_INVALID', `field ${fieldNo} vanished from compiled graph`);
      }
      frame.pendingField = {
        fieldNo: field.no,
        typeNo: field.typeNo,
        unknown: false,
        migrationPath: null,
      };
      return;
    }

    if (this.migration !== null && this.frames.length === 1 && frame.capture === null) {
      const move = this.migration.movesByName.get(key);
      if (move !== undefined) {
        frame.pendingField = {
          fieldNo: move.fromFieldNo,
          typeNo: this.typeAtPath(this.graph.rootTypeNo, move.toPath),
          unknown: false,
          migrationPath: move.toPath,
        };
        return;
      }
    }

    if (this.options.strictUnknown === true) {
      throw new DecodeError('UNKNOWN_FIELD', `unknown property "${key}" in type ${frame.typeNo}`);
    }
    frame.pendingField = { fieldNo: -1, typeNo: -1, unknown: true, migrationPath: null };
  }

  private readValueToken(frame: Frame, token: Token): void {
    const pending = frame.pendingField;

    if (frame.kind === 'object' && pending === null) {
      throw new DecodeError('SYNTAX_ERROR', 'object value without resolved field');
    }

    if (frame.kind === 'object' && pending !== null && pending.unknown) {
      this.readSkippedValue(frame, token);
      return;
    }

    if (frame.kind === 'object' && pending !== null && pending.migrationPath !== null) {
      this.beginCapture(frame, pending, token);
      return;
    }

    if (frame.capture !== null) {
      this.readCaptureToken(frame, token);
      return;
    }

    const valueTypeNo =
      frame.kind === 'array'
        ? this.requireArrayType(frame.typeNo).elementTypeNo
        : (pending as PendingField).typeNo;
    const fieldNo = frame.kind === 'object' ? (pending as PendingField).fieldNo : null;

    if (token.type === 'beginObject' || token.type === 'beginArray') {
      const wantArray = token.type === 'beginArray';
      const valueType = this.graph.types.get(valueTypeNo);
      if (valueType === undefined) {
        throw new DecodeError('SCHEMA_INVALID', `value type ${valueTypeNo} missing`);
      }
      if (wantArray !== (valueType.kind === 'array')) {
        throw new DecodeError('TYPE_MISMATCH', `expected ${valueType.kind}, got ${token.type}`);
      }
      const node = wantArray ? makeArray(valueTypeNo) : makeObject(valueTypeNo);
      if (frame.kind === 'array') {
        if (frame.node.kind !== 'array') {
          throw new DecodeError('SCHEMA_INVALID', 'array frame holds non-array node');
        }
        frame.node.elements.push(node);
      } else {
        this.appendKnownField(frame, fieldNo as number, node);
      }
      this.frames.push({
        kind: wantArray ? 'array' : 'object',
        typeNo: valueTypeNo,
        node,
        seen: [],
        expect: 'value',
        pendingKey: null,
        pendingField: null,
        capture: null,
        nodeFieldNo: frame.kind === 'object' ? (fieldNo as number) : null,
      });
      this.depth += 1;
      return;
    }

    if (!this.isScalarStart(token)) {
      throw new DecodeError('SYNTAX_ERROR', `unexpected structural token ${token.type}`);
    }
    const node = this.scalarNode(valueTypeNo, token);
    if (frame.kind === 'array') {
      if (frame.node.kind !== 'array') {
        throw new DecodeError('SCHEMA_INVALID', 'array frame holds non-array node');
      }
      frame.node.elements.push(node);
    } else {
      this.appendKnownField(frame, fieldNo as number, node);
    }
    frame.expect = 'commaOrEnd';
    frame.pendingKey = null;
    frame.pendingField = null;
  }

  private appendKnownField(frame: Frame, fieldNo: number, node: IdNode): void {
    if (frame.node.kind !== 'object') {
      throw new DecodeError('SCHEMA_INVALID', 'object frame holds non-object node');
    }
    if (frame.seen.includes(fieldNo)) {
      // 两个别名（或规范名与别名）映射到同一字段编号，无论值是否相同都拒绝。
      throw new DecodeError(
        'DUPLICATE_FIELD',
        `field no ${fieldNo} already present in type ${frame.typeNo}`,
      );
    }
    frame.seen.push(fieldNo);
    frame.node.fields.push({ no: fieldNo, value: node });
  }

  private readSkippedValue(frame: Frame, token: Token): void {
    if (token.type === 'beginObject' || token.type === 'beginArray') {
      this.frames.push({
        kind: token.type === 'beginObject' ? 'object' : 'array',
        typeNo: -1,
        node: token.type === 'beginObject' ? makeObject(-1) : makeArray(-1),
        seen: [],
        expect: 'value',
        pendingKey: null,
        pendingField: { fieldNo: -1, typeNo: -1, unknown: true, migrationPath: null },
        capture: null,
        nodeFieldNo: null,
      });
      this.depth += 1;
      return;
    }
    if (!this.isScalarStart(token)) {
      throw new DecodeError('SYNTAX_ERROR', `bad skipped value token ${token.type}`);
    }
    // 未知标量值被丢弃，但语法位置必须推进，否则会吞掉后续冒号/逗号。
    frame.expect = 'commaOrEnd';
    frame.pendingKey = null;
    frame.pendingField = null;
  }

  private closeFrame(): void {
    const closing = this.frames.pop();
    if (closing === undefined) {
      throw new DecodeError('SYNTAX_ERROR', 'closing token without open frame');
    }
    this.depth -= 1;

    if (closing.typeNo === -1) {
      const parent = this.frames[this.frames.length - 1];
      if (parent === undefined) {
        throw new DecodeError('SYNTAX_ERROR', 'unknown value at root');
      }
      if (parent.capture !== null) {
        this.finishCaptureContainer(closing, parent);
        return;
      }
      parent.expect = 'commaOrEnd';
      parent.pendingKey = null;
      parent.pendingField = null;
      return;
    }

    if (closing.capture !== null) {
      const parent = this.frames[this.frames.length - 1];
      if (parent === undefined) {
        throw new DecodeError('SYNTAX_ERROR', 'capture frame without parent');
      }
      this.finishCaptureContainer(closing, parent);
      return;
    }

    const parent = this.frames[this.frames.length - 1];
    if (parent === undefined) {
      this.expect = 'done';
      return;
    }
    if (parent.capture !== null) {
      this.finishCaptureContainer(closing, parent);
      return;
    }
    parent.expect = 'commaOrEnd';
    parent.pendingKey = null;
    parent.pendingField = null;
  }

  /* ---------------------------- 迁移捕获 ---------------------------- */

  private beginCapture(frame: Frame, pending: PendingField, token: Token): void {
    if (this.frames.length !== 1) {
      throw new DecodeError('MIGRATION_CONFLICT', 'migration sources must be root fields');
    }
    const path = pending.migrationPath as number[];
    if (frame.seen.includes(pending.fieldNo)) {
      throw new DecodeError('DUPLICATE_FIELD', `migrated field no ${pending.fieldNo} already present`);
    }
    frame.seen.push(pending.fieldNo);
    const capture: Capture = {
      path,
      fromFieldNo: pending.fieldNo,
      frames: [],
      rootSnapshot: null,
      depth: 0,
    };
    frame.capture = capture;
    frame.pendingField = { ...pending, migrationPath: null };
    this.readCaptureToken(frame, token);
  }

  private readCaptureToken(frame: Frame, token: Token): void {
    const capture = frame.capture as Capture;
    const top = capture.frames[capture.frames.length - 1];
    if (top === undefined) {
      this.captureRootValue(frame, capture, token);
      return;
    }
    if (top.kind === 'array') {
      this.captureArrayToken(frame, capture, top, token);
    } else {
      this.captureObjectToken(frame, capture, top, token);
    }
  }

  private captureRootValue(frame: Frame, capture: Capture, token: Token): void {
    const valueTypeNo = this.typeAtPath(this.graph.rootTypeNo, capture.path);
    if (token.type === 'beginObject' || token.type === 'beginArray') {
      const wantArray = token.type === 'beginArray';
      this.assertTypeShape(valueTypeNo, wantArray, token.type);
      capture.frames.push({
        kind: wantArray ? 'array' : 'object',
        typeNo: valueTypeNo,
        seen: [],
        pendingFieldNo: null,
        scratchArray: [],
        scratchFields: [],
      });
      capture.depth = 1;
      return;
    }
    if (!this.isScalarStart(token)) {
      throw new DecodeError('SYNTAX_ERROR', `bad migration value token ${token.type}`);
    }
    capture.rootSnapshot = this.snapshotScalar(valueTypeNo, token);
    this.completeCapture(frame);
  }

  private captureObjectToken(
    frame: Frame,
    capture: Capture,
    top: CaptureFrame,
    token: Token,
  ): void {
    if (top.pendingFieldNo === null) {
      if (token.type === 'endObject') {
        this.popCaptureContainer(frame, capture);
        return;
      }
      if (token.type !== 'string') {
        throw new DecodeError('SYNTAX_ERROR', 'captured object key must be a string');
      }
      const objectType = this.requireObjectType(top.typeNo);
      const fieldNo = objectType.fieldNoByName.get(token.value);
      if (fieldNo === undefined) {
        if (this.options.strictUnknown === true) {
          throw new DecodeError('UNKNOWN_FIELD', `unknown captured property "${token.value}"`);
        }
        top.pendingFieldNo = -1;
      } else {
        top.pendingFieldNo = fieldNo;
      }
      return;
    }

    if (top.pendingFieldNo === -1) {
      this.captureSkippedToken(frame, capture, top, token);
      return;
    }

    const objectType = this.requireObjectType(top.typeNo);
    const field = objectType.fieldsByNo.get(top.pendingFieldNo);
    if (field === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `captured field ${top.pendingFieldNo} missing`);
    }
    if (token.type === 'beginObject' || token.type === 'beginArray') {
      const wantArray = token.type === 'beginArray';
      this.assertTypeShape(field.typeNo, wantArray, token.type);
      if (top.seen.includes(field.no)) {
        throw new DecodeError('DUPLICATE_FIELD', `captured field no ${field.no} already present`);
      }
      top.seen.push(field.no);
      capture.frames.push({
        kind: wantArray ? 'array' : 'object',
        typeNo: field.typeNo,
        seen: [],
        pendingFieldNo: null,
        scratchArray: [],
        scratchFields: [],
      });
      capture.depth += 1;
      return;
    }
    if (!this.isScalarStart(token)) {
      throw new DecodeError('SYNTAX_ERROR', `bad captured scalar token ${token.type}`);
    }
    if (top.seen.includes(field.no)) {
      throw new DecodeError('DUPLICATE_FIELD', `captured field no ${field.no} already present`);
    }
    top.seen.push(field.no);
    this.attachCaptureScalar(top, field.no, this.snapshotScalar(field.typeNo, token));
    top.pendingFieldNo = null;
  }

  private captureArrayToken(
    frame: Frame,
    capture: Capture,
    top: CaptureFrame,
    token: Token,
  ): void {
    const elementTypeNo = this.requireArrayType(top.typeNo).elementTypeNo;
    if (token.type === 'beginObject' || token.type === 'beginArray') {
      const wantArray = token.type === 'beginArray';
      this.assertTypeShape(elementTypeNo, wantArray, token.type);
      capture.frames.push({
        kind: wantArray ? 'array' : 'object',
        typeNo: elementTypeNo,
        seen: [],
        pendingFieldNo: null,
        scratchArray: [],
        scratchFields: [],
      });
      capture.depth += 1;
      return;
    }
    if (!this.isScalarStart(token)) {
      throw new DecodeError('SYNTAX_ERROR', `bad captured array element token ${token.type}`);
    }
    this.attachCaptureElement(top, this.snapshotScalar(elementTypeNo, token));
  }

  private captureSkippedToken(
    frame: Frame,
    capture: Capture,
    top: CaptureFrame,
    token: Token,
  ): void {
    if (token.type === 'beginObject' || token.type === 'beginArray') {
      capture.frames.push({
        kind: token.type === 'beginObject' ? 'object' : 'array',
        typeNo: -1,
        seen: [],
        pendingFieldNo: -1,
        scratchArray: [],
        scratchFields: [],
      });
      capture.depth += 1;
      return;
    }
    if (!this.isScalarStart(token)) {
      throw new DecodeError('SYNTAX_ERROR', `bad skipped captured token ${token.type}`);
    }
    top.pendingFieldNo = null;
  }

  private popCaptureContainer(frame: Frame, capture: Capture): void {
    const closed = capture.frames.pop();
    if (closed === undefined) {
      throw new DecodeError('SYNTAX_ERROR', 'capture container underflow');
    }
    capture.depth -= 1;
    if (capture.frames.length === 0) {
      capture.rootSnapshot = this.buildContainerSnapshot(closed);
      this.completeCapture(frame);
      return;
    }
    const parent = capture.frames[capture.frames.length - 1];
    const snapshot = this.buildContainerSnapshot(closed);
    if (parent.kind === 'array') {
      this.attachCaptureElement(parent, snapshot);
    } else if (parent.pendingFieldNo !== null && parent.pendingFieldNo !== -1) {
      this.attachCaptureScalar(parent, parent.pendingFieldNo, snapshot);
      parent.pendingFieldNo = null;
    } else if (closed.typeNo === -1) {
      parent.pendingFieldNo = null;
    } else {
      throw new DecodeError('SYNTAX_ERROR', 'captured container without parent slot');
    }
  }

  private finishCaptureContainer(closing: Frame, parent: Frame): void {
    const capture = parent.capture as Capture;
    const closedCapture = capture.frames.pop();
    if (closedCapture === undefined) {
      throw new DecodeError('SYNTAX_ERROR', 'capture finish without capture frame');
    }
    capture.depth -= 1;
    if (closing.typeNo === -1) {
      if (capture.frames.length === 0) {
        throw new DecodeError('SYNTAX_ERROR', 'unknown migration root value');
      }
      const top = capture.frames[capture.frames.length - 1];
      top.pendingFieldNo = null;
      parent.expect = 'commaOrEnd';
      parent.pendingKey = null;
      parent.pendingField = null;
      return;
    }
    if (capture.frames.length === 0) {
      capture.rootSnapshot = this.buildContainerSnapshot(closedCapture);
      this.completeCapture(parent);
      return;
    }
    const top = capture.frames[capture.frames.length - 1];
    const snapshot = this.buildContainerSnapshot(closedCapture);
    if (top.kind === 'array') {
      this.attachCaptureElement(top, snapshot);
    } else if (top.pendingFieldNo !== null && top.pendingFieldNo !== -1) {
      this.attachCaptureScalar(top, top.pendingFieldNo, snapshot);
      top.pendingFieldNo = null;
    } else {
      throw new DecodeError('SYNTAX_ERROR', 'captured container lost parent slot');
    }
    parent.expect = 'commaOrEnd';
    parent.pendingKey = null;
    parent.pendingField = null;
  }

  private completeCapture(frame: Frame): void {
    const capture = frame.capture as Capture;
    if (capture.rootSnapshot === null) {
      throw new DecodeError('SYNTAX_ERROR', 'migration completed without value');
    }
    this.placeMigration(this.rootNode as IdNode, capture.path, capture.fromFieldNo, capture.rootSnapshot);
    frame.capture = null;
    frame.expect = 'commaOrEnd';
    frame.pendingKey = null;
    frame.pendingField = null;
  }

  private placeMigration(
    root: IdNode,
    path: number[],
    fromFieldNo: number,
    snapshot: Snapshot,
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
      // 迁移目标在当前消息里已经被普通字段填充：多段映射冲突，拒绝。
      throw new DecodeError(
        'MIGRATION_CONFLICT',
        `migration destination field ${destinationNo} already populated`,
      );
    }
    if (current === root && root.kind === 'object') {
      const rootFrame = this.frames[0];
      if (rootFrame !== undefined && rootFrame.seen.includes(fromFieldNo) === false) {
        rootFrame.seen.push(fromFieldNo);
      }
    }
    current.fields.push({ no: destinationNo, value: revive(snapshot) });
  }

  private buildContainerSnapshot(frame: CaptureFrame): Snapshot {
    if (frame.kind === 'array') {
      return { kind: 'array', typeNo: frame.typeNo, elements: frame.scratchArray };
    }
    return { kind: 'object', typeNo: frame.typeNo, fields: frame.scratchFields };
  }

  private attachCaptureElement(
    parent: CaptureFrame,
    snapshot: Snapshot,
  ): void {
    if (parent.kind !== 'array') {
      throw new DecodeError('SYNTAX_ERROR', 'array element attached to object capture frame');
    }
    parent.scratchArray.push(snapshot);
  }

  private attachCaptureScalar(
    parent: CaptureFrame,
    fieldNo: number,
    snapshot: Snapshot,
  ): void {
    if (parent.kind !== 'object') {
      throw new DecodeError('SYNTAX_ERROR', 'object field attached to array capture frame');
    }
    parent.scratchFields.push({ no: fieldNo, value: snapshot });
  }

  private assertTypeShape(typeNo: number, wantArray: boolean, tokenType: string): void {
    const valueType = this.graph.types.get(typeNo);
    if (valueType === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `value type ${typeNo} missing`);
    }
    if (wantArray !== (valueType.kind === 'array')) {
      throw new DecodeError('TYPE_MISMATCH', `expected ${valueType.kind}, got ${tokenType}`);
    }
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

  private isScalarStart(token: Token): boolean {
    return (
      token.type === 'string' ||
      token.type === 'number' ||
      token.type === 'bool' ||
      token.type === 'null'
    );
  }

  private scalarNode(typeNo: number, token: Token): IdNode {
    return this.snapshotScalar(typeNo, token) as IdNode;
  }

  private snapshotScalar(typeNo: number, token: Token): Snapshot {
    const type = this.graph.types.get(typeNo);
    if (type === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `scalar type ${typeNo} missing`);
    }
    switch (type.kind) {
      case 'id64':
        if (token.type === 'string') {
          return { kind: 'id64', typeNo, value: canonicalId64FromText(token.value) };
        }
        if (token.type === 'number') {
          return { kind: 'id64', typeNo, value: canonicalId64FromNumberLexeme(token.lexeme) };
        }
        throw new DecodeError('TYPE_MISMATCH', `id64 field got ${token.type}`);
      case 'int32': {
        if (token.type !== 'number') {
          throw new DecodeError('TYPE_MISMATCH', `int32 field got ${token.type}`);
        }
        const canonical = requireCanonicalIntegerLexeme(token.lexeme);
        const numeric = Number(canonical);
        if (!Number.isSafeInteger(numeric) || numeric < I32_MIN || numeric > I32_MAX) {
          throw new DecodeError('RANGE_ERROR', `int32 out of range: ${token.lexeme}`);
        }
        return { kind: 'int32', typeNo, value: numeric };
      }
      case 'string':
        if (token.type !== 'string') {
          throw new DecodeError('TYPE_MISMATCH', `string field got ${token.type}`);
        }
        return { kind: 'string', typeNo, value: token.value };
      case 'bool':
        if (token.type !== 'bool') {
          throw new DecodeError('TYPE_MISMATCH', `bool field got ${token.type}`);
        }
        return { kind: 'bool', typeNo, value: token.value };
      case 'object':
      case 'array':
        throw new DecodeError('TYPE_MISMATCH', `expected container ${type.kind}, got scalar token`);
    }
  }

  private checkRequired(typeNo: number, node: IdNode): void {
    const type = this.graph.types.get(typeNo);
    if (type === undefined) {
      throw new DecodeError('SCHEMA_INVALID', `type ${typeNo} missing in required check`);
    }
    if (type.kind === 'object') {
      const present = new Set(node.kind === 'object' ? node.fields.map((field) => field.no) : []);
      for (const field of type.sortedFields) {
        if (field.required && !present.has(field.no)) {
          throw new DecodeError('TYPE_MISMATCH', `required field ${field.no} missing in type ${typeNo}`);
        }
      }
      if (node.kind === 'object') {
        for (const field of node.fields) {
          this.checkRequired(field.value.typeNo, field.value);
        }
      }
    } else if (type.kind === 'array' && node.kind === 'array') {
      for (const element of node.elements) {
        this.checkRequired(element.typeNo, element);
      }
    }
  }
}

const CANON_INTEGER = /^-?(?:0|[1-9][0-9]*)$/;

function requireCanonicalIntegerLexeme(lexeme: string): string {
  if (!CANON_INTEGER.test(lexeme)) {
    throw new DecodeError('TYPE_MISMATCH', `non-canonical integer lexeme: ${lexeme}`);
  }
  if (lexeme === '-0') {
    throw new DecodeError('TYPE_MISMATCH', 'ambiguous -0 integer lexeme');
  }
  return lexeme;
}

function captureCheckpoint(capture: Capture): CaptureCheckpoint {
  return {
    path: [...capture.path],
    fromFieldNo: capture.fromFieldNo,
    frames: capture.frames.map((frame) => ({
      kind: frame.kind,
      typeNo: frame.typeNo,
      seen: [...frame.seen],
      pendingFieldNo: frame.pendingFieldNo,
      scratchArray: frame.scratchArray.map((snapshot) => snapshot),
      scratchFields: frame.scratchFields.map((entry) => ({
        no: entry.no,
        value: entry.value,
      })),
    })),
    rootSnapshot: capture.rootSnapshot,
    depth: capture.depth,
  };
}

function restoreCapture(cp: CaptureCheckpoint): Capture {
  return {
    path: [...cp.path],
    fromFieldNo: cp.fromFieldNo,
    frames: cp.frames.map((frame) => ({
      kind: frame.kind,
      typeNo: frame.typeNo,
      seen: [...frame.seen],
      pendingFieldNo: frame.pendingFieldNo,
      scratchArray: frame.scratchArray.map((snapshot) => snapshot),
      scratchFields: frame.scratchFields.map((entry) => ({
        no: entry.no,
        value: entry.value,
      })),
    })),
    rootSnapshot: cp.rootSnapshot,
    depth: cp.depth,
  };
}

export function snapshotOf(node: IdNode): Snapshot {
  switch (node.kind) {
    case 'object':
      return {
        kind: 'object',
        typeNo: node.typeNo,
        fields: node.fields.map((field) => ({ no: field.no, value: snapshotOf(field.value) })),
      };
    case 'array':
      return {
        kind: 'array',
        typeNo: node.typeNo,
        elements: node.elements.map(snapshotOf),
      };
    default:
      return { ...node };
  }
}

export function revive(snapshot: Snapshot): IdNode {
  switch (snapshot.kind) {
    case 'object':
      return {
        kind: 'object',
        typeNo: snapshot.typeNo,
        fields: snapshot.fields.map((field) => ({ no: field.no, value: revive(field.value) })),
      };
    case 'array':
      return {
        kind: 'array',
        typeNo: snapshot.typeNo,
        elements: snapshot.elements.map(revive),
      };
    default:
      return { ...snapshot };
  }
}
