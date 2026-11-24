import type {
  RuntimeState,
  HandleRecord,
  ObjectRecord,
} from "./model.ts";
import type {
  SerializedHandle,
  SerializedObject,
  SerializedState,
} from "./types.ts";
import { isProtectionClass } from "./types.ts";

function compare(left: string, right: string): number {
  if (left === right) {
    return 0;
  }
  return left < right ? -1 : 1;
}

export function serializeState(
  state: RuntimeState,
  id: number,
): SerializedState {
  const objects: SerializedObject[] = [...state.objects.values()]
    .sort((left, right) => compare(left.id, right.id))
    .map((object) => ({
      id: object.id,
      protectionClass: object.protectionClass,
      contentLabel: object.contentLabel,
      volume: object.volume,
    }));
  const paths = [...state.paths.entries()].sort((left, right) =>
    compare(left[0], right[0]),
  );
  const handles: SerializedHandle[] = [...state.handles.values()]
    .sort((left, right) => compare(left.id, right.id))
    .map((handle) => ({
      id: handle.id,
      objectId: handle.objectId,
      openedAtLockSerial: handle.openedAtLockSerial,
      valid: handle.valid,
    }));
  return {
    id,
    objects,
    paths,
    handles,
    locked: state.locked,
    lockSerial: state.lockSerial,
    executed: [...state.executed].sort(compare),
  };
}

export function deserializeState(serialized: SerializedState): RuntimeState {
  if (serialized === null || typeof serialized !== "object") {
    throw new Error("state is not an object");
  }
  if (!Array.isArray(serialized.objects)) {
    throw new Error("state.objects is not an array");
  }
  const objects = new Map<string, ObjectRecord>();
  for (const object of serialized.objects) {
    if (object === null || typeof object !== "object") {
      throw new Error("state object entry is not an object");
    }
    if (objects.has(object.id)) {
      throw new Error(`duplicate object id ${object.id}`);
    }
    if (
      !isProtectionClass(object.protectionClass) ||
      !isProtectionClass(object.contentLabel)
    ) {
      throw new Error(`object ${object.id} has an invalid protection class`);
    }
    if (typeof object.volume !== "string" || object.volume.length === 0) {
      throw new Error(`object ${object.id} has an invalid volume`);
    }
    objects.set(object.id, {
      id: object.id,
      protectionClass: object.protectionClass,
      contentLabel: object.contentLabel,
      volume: object.volume,
    });
  }
  if (!Array.isArray(serialized.paths)) {
    throw new Error("state.paths is not an array");
  }
  const paths = new Map<string, string>();
  for (const entry of serialized.paths) {
    if (!Array.isArray(entry) || entry.length !== 2) {
      throw new Error("state path entry is malformed");
    }
    if (paths.has(entry[0])) {
      throw new Error(`duplicate state path ${entry[0]}`);
    }
    if (!objects.has(entry[1])) {
      throw new Error(`path ${entry[0]} references missing object ${entry[1]}`);
    }
    paths.set(entry[0], entry[1]);
  }
  if (!Array.isArray(serialized.handles)) {
    throw new Error("state.handles is not an array");
  }
  const handles = new Map<string, HandleRecord>();
  for (const handle of serialized.handles) {
    if (handle === null || typeof handle !== "object") {
      throw new Error("state handle entry is not an object");
    }
    if (handles.has(handle.id)) {
      throw new Error(`duplicate handle id ${handle.id}`);
    }
    if (typeof handle.valid !== "boolean") {
      throw new Error(`handle ${handle.id} has an invalid valid flag`);
    }
    if (
      typeof handle.openedAtLockSerial !== "number" ||
      !Number.isInteger(handle.openedAtLockSerial) ||
      handle.openedAtLockSerial < 0
    ) {
      throw new Error(`handle ${handle.id} has an invalid lock serial`);
    }
    if (!objects.has(handle.objectId)) {
      throw new Error(
        `handle ${handle.id} references missing object ${handle.objectId}`,
      );
    }
    handles.set(handle.id, {
      id: handle.id,
      objectId: handle.objectId,
      openedAtLockSerial: handle.openedAtLockSerial,
      valid: handle.valid,
    });
  }
  if (!Array.isArray(serialized.executed)) {
    throw new Error("state.executed is not an array");
  }
  const executed = new Set<string>();
  for (const event of serialized.executed) {
    if (typeof event !== "string") {
      throw new Error("state.executed contains a non-string event id");
    }
    if (executed.has(event)) {
      throw new Error(`duplicate executed event ${event}`);
    }
    executed.add(event);
  }
  if (typeof serialized.locked !== "boolean") {
    throw new Error("state.locked is not a boolean");
  }
  if (
    typeof serialized.lockSerial !== "number" ||
    !Number.isInteger(serialized.lockSerial) ||
    serialized.lockSerial < 0
  ) {
    throw new Error("state.lockSerial is not a non-negative integer");
  }
  return {
    objects,
    paths,
    handles,
    locked: serialized.locked,
    lockSerial: serialized.lockSerial,
    executed,
  };
}
