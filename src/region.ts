import { createHash, generateKeyPairSync, sign, verify } from "node:crypto";
import type { Ballot, RegionConfig, SignedVote } from "./types.ts";

/** 规范化序列化：键序固定，保证签名内容可复算 */
export function canonicalize(ballot: Ballot): string {
  return JSON.stringify({
    decision: ballot.decision,
    epoch: ballot.epoch,
    topology: ballot.topology,
    voter: ballot.voter,
  });
}

export function sha256Hex(data: string): string {
  return createHash("sha256").update(data, "utf8").digest("hex");
}

/**
 * 区域（数据中心）：
 * - 持有独立权重与本地多数派门限；
 * - 只有本地赞成节点数达到门限时，其权重才计入全局仲裁；
 * - 持有 Ed25519 密钥对，对选票进行签名，形成可验证证据。
 */
export class Region {
  readonly id: string;
  readonly weight: number;
  readonly localNodes: number;
  readonly localMajorityThreshold: number;
  readonly isPrimary: boolean;
  readonly publicKey: string;

  private readonly privateKey: string;
  /** 当前可达性（由心跳/健康检查维护） */
  reachable = true;
  /** 本地投赞成票的节点数 */
  localYesVotes: number;

  constructor(config: RegionConfig) {
    if (config.weight < 1) throw new Error(`区域 ${config.id} 权重必须 >= 1`);
    if (config.localMajorityThreshold > config.localNodes) {
      throw new Error(`区域 ${config.id} 本地门限超过节点总数`);
    }
    this.id = config.id;
    this.weight = config.weight;
    this.localNodes = config.localNodes;
    this.localMajorityThreshold = config.localMajorityThreshold;
    this.isPrimary = config.isPrimary ?? false;
    this.localYesVotes = config.localNodes;
    const { publicKey, privateKey } = generateKeyPairSync("ed25519");
    this.publicKey = publicKey.export({ type: "spki", format: "pem" }) as string;
    this.privateKey = privateKey.export({ type: "pkcs8", format: "pem" }) as string;
  }

  /** 本地多数派是否达成 */
  localQuorumSatisfied(): boolean {
    return this.reachable && this.localYesVotes >= this.localMajorityThreshold;
  }

  /** 对选票签名；本地多数派未达成时拒绝投票 */
  castVote(ballot: Ballot): SignedVote | null {
    if (!this.localQuorumSatisfied()) return null;
    const payload = canonicalize(ballot);
    const signature = sign(null, Buffer.from(payload, "utf8"), this.privateKey);
    return {
      regionId: this.id,
      weight: this.weight,
      signature: signature.toString("hex"),
      publicKey: this.publicKey,
    };
  }
}

/** 使用投票中携带的公钥独立验证签名 */
export function verifyVote(ballot: Ballot, vote: SignedVote): boolean {
  const payload = canonicalize(ballot);
  return verify(
    null,
    Buffer.from(payload, "utf8"),
    vote.publicKey,
    Buffer.from(vote.signature, "hex"),
  );
}
