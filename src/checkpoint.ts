import { readFile, writeFile, mkdir, rename } from "node:fs/promises";
import { dirname } from "node:path";
import type { SerializedError } from "./errors.ts";

/** 检查点快照：已产出记录 ID 与错误台账，保证恢复后不重复产出、不重复计数。 */
export interface CheckpointData {
  version: 1;
  pipelineId: string;
  /** 已处理到的最大序号。 */
  lastSeq: number;
  /** 已成功产出的记录 ID（产出即登记，恢复时跳过）。 */
  emittedIds: string[];
  /** 错误台账，按 recordId 去重。 */
  errors: SerializedError[];
}

export interface CheckpointStore {
  load(pipelineId: string): Promise<CheckpointData | undefined>;
  save(data: CheckpointData): Promise<void>;
}

export function emptyCheckpoint(pipelineId: string): CheckpointData {
  return { version: 1, pipelineId, lastSeq: -1, emittedIds: [], errors: [] };
}

export class MemoryCheckpointStore implements CheckpointStore {
  private readonly data = new Map<string, CheckpointData>();

  async load(pipelineId: string): Promise<CheckpointData | undefined> {
    const found = this.data.get(pipelineId);
    return found ? structuredClone(found) : undefined;
  }

  async save(data: CheckpointData): Promise<void> {
    this.data.set(data.pipelineId, structuredClone(data));
  }
}

/** 文件检查点：先写临时文件再原子改名，避免崩溃留下半截文件。 */
export class FileCheckpointStore implements CheckpointStore {
  private readonly file: string;

  constructor(file: string) {
    this.file = file;
  }

  async load(pipelineId: string): Promise<CheckpointData | undefined> {
    let raw: string;
    try {
      raw = await readFile(this.file, "utf8");
    } catch {
      return undefined;
    }
    const data = JSON.parse(raw) as CheckpointData;
    return data.pipelineId === pipelineId ? data : undefined;
  }

  async save(data: CheckpointData): Promise<void> {
    await mkdir(dirname(this.file), { recursive: true });
    const tmp = `${this.file}.tmp`;
    await writeFile(tmp, JSON.stringify(data, null, 2), "utf8");
    await rename(tmp, this.file);
  }
}
