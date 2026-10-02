// VersionEdit: the atomic unit of VersionSet evolution.
// Every state transition (flush, compaction) is described by one edit
// and appended to the MANIFEST as a single checksummed record.

export interface FileMetaData {
  number: number;      // unique file number, e.g. 000123.sst
  fileSize: number;    // bytes
  smallest: string;    // smallest key (inclusive)
  largest: string;     // largest key (inclusive)
  checksum: number;    // crc32 of the SSTable file contents
}

export interface VersionEdit {
  nextFileNumber?: number;
  deletedFiles: Array<[level: number, fileNumber: number]>;
  newFiles: Array<[level: number, file: FileMetaData]>;
}

export function emptyEdit(): VersionEdit {
  return { deletedFiles: [], newFiles: [] };
}

export function encodeEdit(edit: VersionEdit): Buffer {
  return Buffer.from(JSON.stringify(edit), 'utf8');
}

export function decodeEdit(buf: Buffer): VersionEdit {
  const raw = JSON.parse(buf.toString('utf8'));
  if (typeof raw !== 'object' || raw === null) {
    throw new Error('invalid VersionEdit: not an object');
  }
  const edit: VersionEdit = { deletedFiles: [], newFiles: [] };
  if (raw.nextFileNumber !== undefined) {
    if (typeof raw.nextFileNumber !== 'number') throw new Error('invalid nextFileNumber');
    edit.nextFileNumber = raw.nextFileNumber;
  }
  for (const d of raw.deletedFiles ?? []) {
    if (!Array.isArray(d) || d.length !== 2) throw new Error('invalid deletedFiles entry');
    edit.deletedFiles.push([d[0], d[1]]);
  }
  for (const n of raw.newFiles ?? []) {
    if (!Array.isArray(n) || n.length !== 2) throw new Error('invalid newFiles entry');
    const f = n[1];
    if (
      typeof f.number !== 'number' || typeof f.fileSize !== 'number' ||
      typeof f.smallest !== 'string' || typeof f.largest !== 'string' ||
      typeof f.checksum !== 'number'
    ) {
      throw new Error('invalid FileMetaData');
    }
    edit.newFiles.push([n[0], { number: f.number, fileSize: f.fileSize, smallest: f.smallest, largest: f.largest, checksum: f.checksum }]);
  }
  return edit;
}
