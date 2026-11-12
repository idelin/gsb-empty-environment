/**
 * 共享缓冲区安全快照读取。
 *
 * 威胁模型：Worker 通过 SharedArrayBuffer 传入图像块，攻击者可在主线程
 * 校验之后继续改写长度与内容（TOCTOU）。本模块的防御策略：
 *
 * 1. 调用方先登记范围（registerRange / registerView），范围用当时的
 *    byteLength 判断，越界的视图/范围一律拒绝；
 * 2. 每次读取只复制登记范围内的快照到私有 ArrayBuffer，复制时
 *    buffer.byteLength 只读一次，长度与偏移在整个操作期间固定；
 * 3. 校验只针对复制后的快照，业务代码只使用快照，绝不回读共享缓冲区。
 */

export type SnapshotBuffer = ArrayBuffer | SharedArrayBuffer;

export interface SnapshotRange {
  readonly buffer: SnapshotBuffer;
  readonly byteOffset: number;
  readonly byteLength: number;
}

export class SnapshotValidationError extends Error {
  constructor(message = "快照校验失败") {
    super(message);
    this.name = "SnapshotValidationError";
  }
}

function isSnapshotBuffer(value: unknown): value is SnapshotBuffer {
  return (
    value instanceof ArrayBuffer ||
    (typeof SharedArrayBuffer !== "undefined" && value instanceof SharedArrayBuffer)
  );
}

function assertRangeFits(bufferByteLength: number, byteOffset: number, byteLength: number): void {
  if (!Number.isSafeInteger(byteOffset) || byteOffset < 0) {
    throw new RangeError(`byteOffset 必须是非负安全整数，收到: ${String(byteOffset)}`);
  }
  if (!Number.isSafeInteger(byteLength) || byteLength < 0) {
    throw new RangeError(`byteLength 必须是非负安全整数，收到: ${String(byteLength)}`);
  }
  // 用减法而非 byteOffset + byteLength 比较，避免偏移溢出安全整数范围
  if (byteOffset > bufferByteLength - byteLength) {
    throw new RangeError(
      `范围越界: byteOffset=${byteOffset} byteLength=${byteLength} 超出当前 buffer.byteLength=${bufferByteLength}`,
    );
  }
}

/**
 * 登记一个读取范围。范围用调用当时的 buffer.byteLength 判断，
 * 越界（含零长度但偏移超过末尾）一律抛 RangeError。
 * byteLength 省略时取“从 byteOffset 到登记时缓冲区末尾”。
 */
export function registerRange(
  buffer: SnapshotBuffer,
  byteOffset = 0,
  byteLength?: number,
): SnapshotRange {
  if (!isSnapshotBuffer(buffer)) {
    throw new TypeError("buffer 必须是 ArrayBuffer 或 SharedArrayBuffer");
  }
  const currentByteLength = buffer.byteLength;
  const effectiveLength = byteLength === undefined ? currentByteLength - byteOffset : byteLength;
  assertRangeFits(currentByteLength, byteOffset, effectiveLength);
  return Object.freeze({ buffer, byteOffset, byteLength: effectiveLength });
}

/**
 * 从已有视图（TypedArray / DataView）登记范围。
 * 越界视图（例如底层可缩放缓冲区已缩小）会被拒绝：
 * DataView 的 getter 直接抛错；定长 TypedArray 越界后 getter 静默归零，
 * 这里用 subarray 触发宿主的一致性检查来识别。
 */
export function registerView(view: ArrayBufferView): SnapshotRange {
  if (!ArrayBuffer.isView(view)) {
    throw new TypeError("view 必须是 TypedArray 或 DataView");
  }
  let byteOffset: number;
  let byteLength: number;
  if (view instanceof DataView) {
    try {
      byteOffset = view.byteOffset;
      byteLength = view.byteLength;
    } catch {
      throw new RangeError("视图已越界（底层缓冲区可能被缩小），拒绝登记");
    }
  } else {
    try {
      (view as Uint8Array).subarray(0, 0);
    } catch {
      throw new RangeError("视图已越界（底层缓冲区可能被缩小），拒绝登记");
    }
    byteOffset = view.byteOffset;
    byteLength = view.byteLength;
  }
  return registerRange(view.buffer as SnapshotBuffer, byteOffset, byteLength);
}

/**
 * 复制登记范围的快照。buffer.byteLength 只读一次并立即复核范围，
 * 随后把恰好 byteLength 个字节复制到全新的私有 ArrayBuffer。
 * 返回的 Uint8Array 与共享缓冲区无任何关联，之后攻击者的改写不可见。
 */
export function readSnapshot(range: SnapshotRange): Uint8Array {
  const { buffer, byteOffset, byteLength } = range;
  if (!isSnapshotBuffer(buffer)) {
    throw new TypeError("range.buffer 必须是 ArrayBuffer 或 SharedArrayBuffer");
  }
  // 关键：byteLength 只取这一次，整个校验 + 复制过程都使用这个当时的值
  const currentByteLength = buffer.byteLength;
  assertRangeFits(currentByteLength, byteOffset, byteLength);
  let source: Uint8Array;
  try {
    source = new Uint8Array(buffer, byteOffset, byteLength);
  } catch {
    throw new RangeError("登记范围在复制前已越界（缓冲区可能被缩小）");
  }
  const snapshot = new Uint8Array(byteLength);
  snapshot.set(source);
  return snapshot;
}

/**
 * 先复制快照，再对快照校验，校验通过才把快照交给业务。
 * validator 返回 false 时抛 SnapshotValidationError；抛出的异常原样传播。
 * 业务代码只能使用返回的快照，不得再触碰共享缓冲区。
 */
export function readAndValidate(
  range: SnapshotRange,
  validator: (snapshot: Uint8Array) => boolean | void,
): Uint8Array {
  const snapshot = readSnapshot(range);
  const accepted = validator(snapshot);
  if (accepted === false) {
    throw new SnapshotValidationError();
  }
  return snapshot;
}
