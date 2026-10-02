import { BitemporalPriceLedger, FOREVER } from '../src/price-ledger.ts';

const ledger = new BitemporalPriceLedger();

console.log('== 登记规则 ==');
const rule10 = ledger.insert(10, 0, 20, 2);
console.log(`知悉@2：价格 10 分，业务 [0, 20)  -> rule#${rule10}`);
const rule15 = ledger.insert(15, 5, 10, 8);
console.log(`知悉@8：价格 15 分，业务 [5, 10)  -> rule#${rule15}`);

console.log('\n== 双时态查询（验收边界）==');
const queries: Array<[number, number]> = [
  [7, 7],
  [7, 8],
  [10, 8],
];
for (const [validTime, txTime] of queries) {
  console.log(`业务@${validTime} 知悉@${txTime} -> ${ledger.priceAt(validTime, txTime)} 分`);
}

console.log('\n== 撤销 15 分规则 @12 ==');
ledger.revoke(rule15, 12);
for (const txTime of [8, 11, 12]) {
  console.log(`业务@7 知悉@${txTime} -> ${ledger.priceAt(7, txTime) ?? '（无价格）'}`);
}

console.log('\n== 历史切片（审计视角）==');
for (const seg of ledger.history()) {
  const txTo = seg.txTo === FOREVER ? '∞' : String(seg.txTo);
  console.log(
    `rule#${seg.ruleId} ${seg.priceCents}分  业务[${seg.validFrom}, ${seg.validTo})  知悉[${seg.txFrom}, ${txTo})`,
  );
}
