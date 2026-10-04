export class ReassemblyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

export class UnknownVersionError extends ReassemblyError {
  readonly fileId: string;
  readonly version: string;
  constructor(fileId: string, version: string) {
    super(`unknown file version: ${fileId}@${version}`);
    this.fileId = fileId;
    this.version = version;
  }
}

export class SpecConflictError extends ReassemblyError {
  readonly fileId: string;
  readonly version: string;
  constructor(fileId: string, version: string) {
    super(`conflicting spec re-registered for ${fileId}@${version}`);
    this.fileId = fileId;
    this.version = version;
  }
}

export class SegmentOutOfBoundsError extends ReassemblyError {
  readonly offset: number;
  readonly length: number;
  readonly size: number;
  constructor(fileId: string, version: string, offset: number, length: number, size: number) {
    super(`segment [${offset}, ${offset + length}) is outside file bounds [0, ${size}) for ${fileId}@${version}`);
    this.offset = offset;
    this.length = length;
    this.size = size;
  }
}

export class SegmentConflictError extends ReassemblyError {
  readonly conflictOffset: number;
  constructor(fileId: string, version: string, conflictOffset: number) {
    super(`overlapping segment disagrees with trusted bytes at offset ${conflictOffset} for ${fileId}@${version}; version quarantined`);
    this.conflictOffset = conflictOffset;
  }
}

export class VersionNotWritableError extends ReassemblyError {
  readonly state: string;
  constructor(fileId: string, version: string, state: string) {
    super(`version ${fileId}@${version} is ${state} and no longer accepts segments`);
    this.state = state;
  }
}
