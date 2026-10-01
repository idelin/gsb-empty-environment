import { PriceBook } from "./priceBook.ts";

const book = new PriceBook();

// 价格10：业务 [0,20)，知悉于 2
const rule10 = book.addRule({ priceCents: 10, validFrom: 0, validTo: 20, txFrom: 2 });
// 价格15：业务 [5,10)，知悉于 8
const rule15 = book.addRule({ priceCents: 15, validFrom: 5, validTo: 10, txFrom: 8 });

console.log("== 验收边界 ==");
console.log("业务7 @ 知悉7  =>", book.priceAt(7, 7)); // 10
console.log("业务7 @ 知悉8  =>", book.priceAt(7, 8)); // 15
console.log("业务10 @ 知悉8 =>", book.priceAt(10, 8)); // 10（[5,10) 右开）

console.log("\n== 撤销演示：知悉时刻12 撤销价格15 规则 ==");
book.retract(rule15, 12);
console.log("业务7 @ 知悉11 =>", book.priceAt(7, 11)); // 15（撤销前的历史查询不受影响）
console.log("业务7 @ 知悉12 =>", book.priceAt(7, 12)); // 10（撤销后回落到价格10）

console.log("\n== 撤销价格10 规则（知悉时刻15）==");
book.retract(rule10, 15);
console.log("业务3 @ 知悉14 =>", book.priceAt(3, 14)); // 10
console.log("业务3 @ 知悉15 =>", book.priceAt(3, 15)); // null
