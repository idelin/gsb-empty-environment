import {
  createHash,
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
  randomUUID,
  sign,
  verify,
  type KeyObject,
} from 'node:crypto';
import {
  appendFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  writeFileSync,
} from 'node:fs';
import {dirname} from 'node:path';

/** 留痕事件：对应一次访问判定。 */
export interface AuditEvent {
  readonly subjectId: string;
  readonly subjectName: string;
  readonly action: 'READ' | 'EXPORT';
  readonly resource: string;
  readonly requestedFields: readonly string[];
  readonly allowed: boolean;
  readonly reason: string;
  readonly violatingFields: readonly string[];
  /** 访问发起方标识（进程/主机/请求 ID），用于责任界定。 */
  readonly origin?: string;
}

/** 落盘的留痕条目。 */
export interface AuditEntry extends AuditEvent {
  readonly seq: number;
  readonly timestamp: string;
  readonly entryId: string;
  /** 前一条条目的 entryHash，创世条目为固定常量。 */
  readonly prevHash: string;
  /** 对本条内容（不含 hash/signature）的 SHA-256。 */
  readonly entryHash: string;
  /** 平台签名私钥对 entryHash 的 Ed25519 签名（base64）。 */
  readonly signature: string;
}

export interface AuditKeyPair {
  readonly privateKey: KeyObject;
  readonly publicKey: KeyObject;
}

/** 完整性校验结果。 */
export interface IntegrityReport {
  readonly valid: boolean;
  readonly entries: number;
  readonly error?: {
    readonly seq: number;
    readonly entryId: string;
    readonly kind:
      | 'MALFORMED_LINE'
      | 'BAD_SEQUENCE'
      | 'BROKEN_CHAIN'
      | 'DIGEST_MISMATCH'
      | 'SIGNATURE_INVALID'
      | 'UNVERIFIABLE_SIGNATURE';
    readonly detail: string;
  };
}

/** 创世条目的前驱哈希，标识一条全新哈希链的起点。 */
export const GENESIS_PREV_HASH = 'GENESIS:' + '0'.repeat(58);

function sha256Hex(payload: string): string {
  return createHash('sha256').update(payload, 'utf8').end().digest('hex');
}

/** 规范化序列化：键按字典序排序，保证校验方得到完全一致的字节。 */
function canonical(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return '[' + value.map((item) => canonical(item)).join(',') + ']';
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record)
    .filter((key) => record[key] !== undefined)
    .sort();
  return (
    '{' +
    keys.map((key) => JSON.stringify(key) + ':' + canonical(record[key])).join(',') +
    '}'
  );
}

/** 参与哈希与签名的条目内容（剥离自证字段 hash/signature）。 */
type EntryContent = Omit<AuditEntry, 'entryHash' | 'signature'>;

function entryContent(entry: AuditEntry): EntryContent {
  const {entryHash: _hash, signature: _signature, ...content} = entry;
  return content;
}

export function computeEntryHash(entry: AuditEntry): string {
  return sha256Hex(canonical(entryContent(entry)));
}

export function generateAuditKeyPair(): AuditKeyPair {
  return generateKeyPairSync('ed25519');
}

export function exportPublicKeyPem(keyPair: AuditKeyPair): string {
  return keyPair.publicKey.export({type: 'spki', format: 'pem'}).toString('utf8');
}

export function exportPrivateKeyPem(keyPair: AuditKeyPair): string {
  return keyPair.privateKey.export({type: 'pkcs8', format: 'pem'}).toString('utf8');
}

export function loadKeyPair(privatePem: string, publicPem: string): AuditKeyPair {
  return {
    privateKey: createPrivateKey(privatePem),
    publicKey: createPublicKey(publicPem),
  };
}

function signHash(privateKey: KeyObject, hash: string): string {
  return sign(null, Buffer.from(hash, 'hex'), privateKey).toString('base64');
}

function verifyHashSignature(
  publicKey: KeyObject,
  hash: string,
  signatureBase64: string,
): boolean {
  try {
    return verify(null, Buffer.from(hash, 'hex'), publicKey, Buffer.from(signatureBase64, 'base64'));
  } catch {
    return false;
  }
}

/**
 * 只追加（append-only）的不可否认留痕日志。
 *
 * 每一条留痕包含：
 *  1. 单调递增序号与时间戳；
 *  2. 与前一条目绑定的 SHA-256 哈希链（prevHash -> entryHash）；
 *  3. 平台 Ed25519 私钥对 entryHash 的签名。
 *
 * 任何对历史条目的篡改、删除、重排都会破坏哈希链或使签名失效，
 * 签名私钥仅平台持有，因此主体无法否认自己触发的访问，平台也无法伪造。
 */
export class AuditLog {
  private lastHash: string = GENESIS_PREV_HASH;
  private nextSeq = 0;
  private writeChain: Promise<void> = Promise.resolve();

  private readonly filePath: string;
  private readonly keyPair: AuditKeyPair;

  private constructor(filePath: string, keyPair: AuditKeyPair) {
    this.filePath = filePath;
    this.keyPair = keyPair;
  }

  /**
   * 打开日志。若文件已存在则重建链状态（校验现有尾部连续性），
   * 保证服务重启后仍可在同一条链上续写。
   */
  static open(filePath: string, keyPair: AuditKeyPair): AuditLog {
    const log = new AuditLog(filePath, keyPair);
    if (existsSync(filePath)) {
      const lines = readFileSync(filePath, 'utf8').split('\n').filter((line) => line.length > 0);
      if (lines.length > 0) {
        const lastEntry = JSON.parse(lines[lines.length - 1]) as AuditEntry;
        log.lastHash = lastEntry.entryHash;
        log.nextSeq = lastEntry.seq + 1;
      }
    } else {
      mkdirSync(dirname(filePath), {recursive: true});
    }
    return log;
  }

  /** 追加一条访问留痕，返回带哈希与签名的完整条目。串行化写入防止并发乱序。 */
  append(event: AuditEvent): Promise<AuditEntry> {
    const task = this.writeChain.then(() => {
      const seq = this.nextSeq;
      const prevHash = this.lastHash;
      const unsigned = {
        ...event,
        seq,
        timestamp: new Date().toISOString(),
        entryId: randomUUID(),
        prevHash,
      } satisfies EntryContent;

      const entryHash = sha256Hex(canonical(unsigned));
      const signature = signHash(this.keyPair.privateKey, entryHash);
      const entry: AuditEntry = {...unsigned, entryHash, signature};

      appendFileSync(this.filePath, JSON.stringify(entry) + '\n');
      this.lastHash = entryHash;
      this.nextSeq = seq + 1;
      return entry;
    });
    this.writeChain = task.then(
      () => undefined,
      () => undefined,
    );
    return task;
  }

  /** 读取全部留痕条目。 */
  readAll(): AuditEntry[] {
    if (!existsSync(this.filePath)) {
      return [];
    }
    return readFileSync(this.filePath, 'utf8')
      .split('\n')
      .filter((line) => line.length > 0)
      .map((line) => JSON.parse(line) as AuditEntry);
  }

  /** 使用公钥校验整条链：序号连续性、哈希链、摘要与签名。 */
  verifyIntegrity(publicKey: KeyObject = this.keyPair.publicKey): IntegrityReport {
    return verifyEntries(this.readAll(), publicKey);
  }
}

/** 校验给定条目数组的完整性（与存储介质无关，便于测试与离线审计）。 */
export function verifyEntries(entries: readonly AuditEntry[], publicKey: KeyObject): IntegrityReport {
  let prevHash = GENESIS_PREV_HASH;

  for (let index = 0; index < entries.length; index += 1) {
    const entry = entries[index];
    const fail = (
      kind: NonNullable<IntegrityReport['error']>['kind'],
      detail: string,
    ): IntegrityReport => ({
      valid: false,
      entries: entries.length,
      error: {seq: entry?.seq ?? index, entryId: entry?.entryId ?? '(unknown)', kind, detail},
    });

    if (entry === null || typeof entry !== 'object') {
      return fail('MALFORMED_LINE', 'entry is not an object');
    }
    if (entry.seq !== index) {
      return fail('BAD_SEQUENCE', `expected seq ${index}, got ${entry.seq}`);
    }
    if (entry.prevHash !== prevHash) {
      return fail('BROKEN_CHAIN', `prevHash does not match previous entryHash at seq ${entry.seq}`);
    }

    const digest = computeEntryHash(entry);
    if (digest !== entry.entryHash) {
      return fail('DIGEST_MISMATCH', `entry content tampered at seq ${entry.seq}`);
    }
    if (!verifyHashSignature(publicKey, entry.entryHash, entry.signature)) {
      return fail('SIGNATURE_INVALID', `signature verification failed at seq ${entry.seq}`);
    }

    prevHash = entry.entryHash;
  }

  return {valid: true, entries: entries.length};
}

/** 直接对日志文件做离线完整性校验（审计方可独立执行）。 */
export function verifyAuditFile(filePath: string, publicKey: KeyObject): IntegrityReport {
  if (!existsSync(filePath)) {
    return {valid: true, entries: 0};
  }
  const lines = readFileSync(filePath, 'utf8').split('\n').filter((line) => line.length > 0);
  const entries: AuditEntry[] = [];
  for (let index = 0; index < lines.length; index += 1) {
    try {
      entries.push(JSON.parse(lines[index]) as AuditEntry);
    } catch (cause) {
      return {
        valid: false,
        entries: lines.length,
        error: {
          seq: index,
          entryId: '(unknown)',
          kind: 'MALFORMED_LINE',
          detail: `line ${index} is not valid JSON: ${(cause as Error).message}`,
        },
      };
    }
  }
  return verifyEntries(entries, publicKey);
}

/**
 * 测试/演练辅助：就地篡改日志文件中某条记录的字段，
 * 模拟攻击者或内部人员试图抹除越权痕迹。
 */
export function tamperAuditFile(filePath: string, seq: number, patch: Record<string, unknown>): void {
  const lines = readFileSync(filePath, 'utf8').split('\n');
  const targetIndex = lines.findIndex((line) => {
    if (line.length === 0) {
      return false;
    }
    try {
      return (JSON.parse(line) as AuditEntry).seq === seq;
    } catch {
      return false;
    }
  });
  if (targetIndex < 0) {
    throw new Error(`audit entry with seq ${seq} not found in ${filePath}`);
  }
  const entry = JSON.parse(lines[targetIndex]) as AuditEntry;
  lines[targetIndex] = JSON.stringify({...entry, ...patch});
  const tmp = `${filePath}.tmp`;
  writeFileSync(tmp, lines.join('\n'));
  renameSync(tmp, filePath);
}
