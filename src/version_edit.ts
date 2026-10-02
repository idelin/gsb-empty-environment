export interface FileMetaData {
  fileNumber: number;
  fileSize: number;
  smallestKey: string;
  largestKey: string;
  checksum: number;
}

export interface AddedFile {
  level: number;
  meta: FileMetaData;
}

export interface DeletedFile {
  level: number;
  fileNumber: number;
}

interface EditPayload {
  addedFiles: AddedFile[];
  deletedFiles: DeletedFile[];
  nextFileNumber?: number;
}

export class VersionEdit {
  addedFiles: AddedFile[] = [];
  deletedFiles: DeletedFile[] = [];
  nextFileNumber?: number;

  addFile(level: number, meta: FileMetaData): void {
    this.addedFiles.push({ level, meta });
  }

  deleteFile(level: number, fileNumber: number): void {
    this.deletedFiles.push({ level, fileNumber });
  }

  encode(): Buffer {
    const payload: EditPayload = {
      addedFiles: this.addedFiles,
      deletedFiles: this.deletedFiles,
    };
    if (this.nextFileNumber !== undefined) payload.nextFileNumber = this.nextFileNumber;
    return Buffer.from(JSON.stringify(payload), "utf8");
  }

  static decode(buf: Buffer): VersionEdit {
    const payload = JSON.parse(buf.toString("utf8")) as EditPayload;
    const edit = new VersionEdit();
    edit.addedFiles = payload.addedFiles ?? [];
    edit.deletedFiles = payload.deletedFiles ?? [];
    edit.nextFileNumber = payload.nextFileNumber;
    return edit;
  }
}
