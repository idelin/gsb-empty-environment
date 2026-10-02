import { appendFile, readFile, rm } from "node:fs/promises";

export interface LogRecord {
  lsn: number;
  streamId: string;
  payload: string;
}

/** 持久化层抽象：追加写 + 全量读回（用于校验）。 */
export interface Storage {
  append(record: LogRecord): Promise<void>;
  readAll(): Promise<LogRecord[]>;
}

/** 基于 JSONL 文件的落盘实现。调用方（管道）保证串行追加。 */
export class FileStorage implements Storage {
  private readonly path: string;

  constructor(path: string) {
    this.path = path;
  }

  async append(record: LogRecord): Promise<void> {
    await appendFile(this.path, JSON.stringify(record) + "\n", "utf8");
  }

  async readAll(): Promise<LogRecord[]> {
    let text: string;
    try {
      text = await readFile(this.path, "utf8");
    } catch {
      return [];
    }
    return text
      .split("\n")
      .filter((line) => line.length > 0)
      .map((line) => JSON.parse(line) as LogRecord);
  }

  async reset(): Promise<void> {
    await rm(this.path, { force: true });
  }
}
