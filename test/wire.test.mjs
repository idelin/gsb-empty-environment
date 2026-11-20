import test from "node:test";
import assert from "node:assert/strict";
import {readWeight,wireWeight} from "../src/weight.ts";
test("archived exact probability encodings",()=>{
 for(const s of ["0/1","1/1","9007199254740993/9007199254740995"])assert.equal(wireWeight(readWeight(s)),s);
 for(const s of ["0/2","2/4","2/1","1/0","1/01","+1/2","1/2\n"])assert.throws(()=>readWeight(s));
});
