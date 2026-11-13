import { generateKeyPairSync, sign } from "node:crypto";
import {
  buildKbJwt,
  hashTransactionData,
  verifyPresentation,
} from "./index.ts";

const encoder = new TextEncoder();

// 钱包展示给用户的交易数据
const displayedTransaction = encoder.encode(
  JSON.stringify({ to: "merchant-a", amount: "12.50", currency: "EUR" }),
);
// 另一份交易数据（攻击者试图调包）
const otherTransaction = encoder.encode(
  JSON.stringify({ to: "attacker-x", amount: "999.00", currency: "EUR" }),
);

const { publicKey: devicePublicKey, privateKey: devicePrivateKey } =
  generateKeyPairSync("ed25519");

function report(label: string, allowed: boolean, reason: string): void {
  console.log(`${label}: ${allowed ? "允许出示" : "拒绝出示"} — ${reason}`);
}

// 场景 1（设备签名）：展示一份交易数据，签名另一份的哈希 → 拒绝
const wrongSignature = sign(null, Buffer.from(hashTransactionData(otherTransaction)), devicePrivateKey);
{
  const { allowed, reason } = verifyPresentation({
    presentedTransactionData: displayedTransaction,
    deviceSignature: wrongSignature,
    devicePublicKey,
  });
  report("场景1 设备签名覆盖另一份交易哈希", allowed, reason);
}

// 场景 2（设备签名）：签名覆盖当前交易字节 → 允许
const correctSignature = sign(null, Buffer.from(hashTransactionData(displayedTransaction)), devicePrivateKey);
{
  const { allowed, reason } = verifyPresentation({
    presentedTransactionData: displayedTransaction,
    deviceSignature: correctSignature,
    devicePublicKey,
  });
  report("场景2 设备签名覆盖当前交易哈希", allowed, reason);
}

// 场景 3（kb-jwt）：kb-jwt 携带另一份交易的哈希 → 拒绝
{
  const kbJwt = buildKbJwt([hashTransactionData(otherTransaction)]);
  const { allowed, reason } = verifyPresentation({
    presentedTransactionData: displayedTransaction,
    kbJwt,
  });
  report("场景3 kb-jwt 覆盖另一份交易哈希", allowed, reason);
}

// 场景 4（kb-jwt）：kb-jwt 覆盖当前交易字节 → 允许
{
  const kbJwt = buildKbJwt([hashTransactionData(displayedTransaction)]);
  const { allowed, reason } = verifyPresentation({
    presentedTransactionData: displayedTransaction,
    kbJwt,
  });
  report("场景4 kb-jwt 覆盖当前交易哈希", allowed, reason);
}

// 场景 5：kb-jwt 缺失交易哈希声明，且无设备签名 → 拒绝
{
  const { allowed, reason } = verifyPresentation({
    presentedTransactionData: displayedTransaction,
  });
  report("场景5 哈希缺失", allowed, reason);
}
