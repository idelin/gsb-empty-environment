import { AnchorService, AnchorLedger, generateAnchorKeys } from "./anchor.ts";
import { CertificateIssuer, verifyCertificate } from "./certificate.ts";

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

const keys = generateAnchorKeys();
const ledger = new AnchorLedger();

show("1. t<1000: 业务事实入库");
const facts = [
  "order#A1001 amount=500 currency=CNY",
  "order#A1002 amount=320 currency=CNY",
  "order#A1003 amount=780 currency=CNY",
  "order#A1004 amount=150 currency=CNY",
];
const issuer = new CertificateIssuer();
issuer.ingest(facts);
console.log("facts ingested:", facts.length);
console.log("merkle root:", issuer.root.toString("hex"));

show("2. t=1000: 定时锚定(根哈希 + 时间戳 + 签名)");
const anchorService = new AnchorService(keys, () => 1000);
const anchor = anchorService.anchor(issuer.root);
console.log("anchor record:", JSON.stringify(anchor, null, 2));
const reg = ledger.register(anchor, keys.publicKey);
console.log("ledger accepted anchor:", reg.ok);

show("3. t=1500: 出示审计凭据(存在性证明)");
const targetFact = facts[1];
const cert = issuer.issue(targetFact, anchor, 1500);
console.log("certificate issued for:", JSON.stringify(targetFact));
console.log("proof steps:", cert.proof.length);

show("4. 验证: 该事实在 t=1000 之前已固定存在");
const result = verifyCertificate(cert, targetFact, keys.publicKey, {
  notLaterThan: 1000,
});
console.log("checks:", result.checks);
console.log("VERIFIED:", result.ok);

show("5. 反例: 篡改数据后验证失败");
const forged = verifyCertificate(
  cert,
  "order#A1002 amount=999 currency=CNY",
  keys.publicKey,
  { notLaterThan: 1000 },
);
console.log("forged data accepted:", forged.ok, "| reason:", forged.reason);

show("6. 反例: 重放同一锚定记录被拒绝");
const replay = ledger.register(anchor, keys.publicKey);
console.log("replay accepted:", replay.ok, "| reason:", replay.reason);

show("7. 反例: 伪造签名被拒绝");
const fakeKeys = generateAnchorKeys();
const fakeService = new AnchorService(fakeKeys, () => 900);
const forgedAnchor = fakeService.anchor(issuer.root);
const forgedReg = ledger.register(forgedAnchor, keys.publicKey);
console.log("forged anchor accepted:", forgedReg.ok, "| reason:", forgedReg.reason);
