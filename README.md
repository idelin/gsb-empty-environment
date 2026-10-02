# merkle-anchor

带有时间戳与防重放签名的默克尔锚定器(外部存证锚定组件)。

定时将本地系统的默克尔根哈希与当前时间戳打包并签名(Ed25519);
支持出示带时间戳证明的存证证书,证明特定数据在特定历史时间点之前已经确定存在。

## 组件

- `src/merkle.ts` — SHA-256 默克尔树(域分离哈希:叶子 `0x00`、节点 `0x01`),证明生成与验证。
- `src/anchor.ts` — `AnchorService`:把 `merkleRoot + timestamp + seq + prevAnchorHash + anchorId`
  打包为规范载荷并做 Ed25519 签名;`AnchorLedger` 负责防重放
  (anchorId 唯一、seq 严格递增、时间戳单调、哈希链连续)。
- `src/certificate.ts` — `CertificateIssuer` 出具审计凭据(叶子哈希 + 默克尔路径 + 锚定记录);
  `verifyCertificate` 验证数据哈希、默克尔证明、锚定签名以及 `timestamp <= 截止时间`。
- `src/demo.ts` — 可运行样例:t=1000 锚定,t=1500 出证,验证存在性,并演示篡改/重放/伪造均被拒绝。

## 运行

要求 Node.js >= 22.6(使用内置类型擦除直接运行 TypeScript,无需编译;Node >= 23.6 可省略 flag)。

```bash
npm run demo   # 运行样例
npm test       # 运行 node:test 内置测试
```

## 验收场景对应

1. t<1000:业务事实入库,构建默克尔树;
2. t=1000:`AnchorService.anchor(root)` 生成带时间戳的签名锚定记录并登记到账本;
3. t=1500:`issuer.issue(fact, anchor, 1500)` 生成审计凭据;
4. `verifyCertificate(cert, fact, pubKey, { notLaterThan: 1000 })` 证明该事实在 t=1000 前已固定;
5. 篡改数据、重放锚定记录、伪造签名均验证失败。
