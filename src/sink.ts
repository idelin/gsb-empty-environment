import { open, type FileHandle } from 'node:fs/promises';

export interface PersistedRecord {
  lsn: number;
  streamId: string;
  payload: Buffer;
}

export interface Sink {
  write(record: PersistedRecord): Promise<void>;
  sync(): Promise<void>;
  close(): Promise<void>;
}

export class FileSink implements Sink {
  private handle: FileHandle | null = null;
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  private async fd(): Promise<FileHandle> {
    if (this.handle === null) {
      this.handle = await open(this.filePath, 'a');
    }
    return this.handle;
  }

  async write(record: PersistedRecord): Promise<void> {
    const line =
      JSON.stringify({
        lsn: record.lsn,
        streamId: record.streamId,
        payload: record.payload.toString('utf8'),
      }) + '\n';
    const handle = await this.fd();
    await handle.appendFile(line, 'utf8');
  }

  async sync(): Promise<void> {
    const handle = await this.fd();
    await handle.sync();
  }

  async close(): Promise<void> {
    if (this.handle !== null) {
      await this.handle.close();
      this.handle = null;
    }
  }
}
