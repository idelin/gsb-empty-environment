import { assertLen, bytesToHex, sha256, utf8 } from "./bytes.ts";
import { ADDRESS_LEN, deriveAddress, SALT_LEN } from "./record.ts";

/**
 * 免责声明：本文件是题内链模型。Destroy 会把代码清空并使地址代次递增，
 * 同一代次内代码不可变。本模型不声称复刻任何特定链当前的自毁
 * （selfdestruct）规则。
 */
export const MODEL_DISCLAIMER =
  "题内链模型：Destroy 清空代码并使地址代次递增，同一代次代码不可变；" +
  "本模型不声称复刻任何特定链当前的自毁（selfdestruct）规则。";

export interface DeployParams {
  deployer: Uint8Array;
  salt: Uint8Array;
  initCode: Uint8Array;
  constructorArgs: Uint8Array;
}

/** 实际部署回调：返回 runtimeCode、创建交易 ID 和块高。 */
export interface DeployCallback {
  success: boolean;
  reason?: string;
  txId: number;
  blockHeight: number;
  address: Uint8Array;
  generation: number;
  runtimeCode: Uint8Array | null;
}

/**
 * 题内“构造器”：runtimeCode 由 initCode 与构造参数共同决定，
 * 因此同一地址（同一 deployer/salt/initCode）在不同代次、
 * 不同构造参数下可能出现不同的 runtimeCode。
 * 空 initCode 产生空 runtimeCode（地址上无代码）。
 */
export function runtimeFor(
  initCode: Uint8Array,
  constructorArgs: Uint8Array,
): Uint8Array {
  if (initCode.length === 0) return new Uint8Array(0);
  return sha256(utf8("runtime-v1"), initCode, constructorArgs);
}

interface Account {
  occupied: boolean;
  code: Uint8Array | null;
  generation: number;
}

export class Chain {
  blockHeight = 0;
  txCount = 0;
  readonly chainId: number;
  private readonly accounts = new Map<string, Account>();

  constructor(chainId: number) {
    if (!Number.isSafeInteger(chainId) || chainId < 0) {
      throw new Error(`invalid chainId: ${chainId}`);
    }
    this.chainId = chainId;
  }

  generationOf(address: Uint8Array): number {
    return this.account(address).generation;
  }

  codeOf(address: Uint8Array): Uint8Array | null {
    return this.account(address).code;
  }

  codeDigestOf(address: Uint8Array): Uint8Array | null {
    const code = this.codeOf(address);
    return code === null ? null : sha256(code);
  }

  deploy(params: DeployParams, opts?: { revert?: boolean }): DeployCallback {
    assertLen(params.deployer, ADDRESS_LEN, "deployer");
    assertLen(params.salt, SALT_LEN, "salt");
    this.txCount += 1;
    this.blockHeight += 1;
    const address = deriveAddress(
      params.deployer,
      params.salt,
      sha256(params.initCode),
    );
    const account = this.account(address);
    if (opts?.revert) {
      // 部署失败：交易上链但状态不变，可用相同参数重试。
      return {
        success: false,
        reason: "init-reverted",
        txId: this.txCount,
        blockHeight: this.blockHeight,
        address,
        generation: account.generation,
        runtimeCode: null,
      };
    }
    if (account.occupied) {
      // 同一代次代码不可变：地址被占用时禁止再次 Deploy。
      throw new Error("address occupied in current generation");
    }
    const runtimeCode = runtimeFor(params.initCode, params.constructorArgs);
    account.occupied = true;
    account.code = runtimeCode.length > 0 ? runtimeCode : null;
    return {
      success: true,
      txId: this.txCount,
      blockHeight: this.blockHeight,
      address,
      generation: account.generation,
      runtimeCode,
    };
  }

  /** Destroy：清空代码，地址代次递增；之后同地址可再次 Deploy。 */
  destroy(address: Uint8Array): void {
    const account = this.account(address);
    if (!account.occupied) {
      throw new Error("no deployed code at address");
    }
    account.occupied = false;
    account.code = null;
    account.generation += 1;
  }

  private account(address: Uint8Array): Account {
    assertLen(address, ADDRESS_LEN, "address");
    const key = bytesToHex(address);
    let account = this.accounts.get(key);
    if (!account) {
      account = { occupied: false, code: null, generation: 0 };
      this.accounts.set(key, account);
    }
    return account;
  }
}
