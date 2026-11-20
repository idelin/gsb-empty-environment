import test from "node:test";
import assert from "node:assert/strict";
import {parseDot,formatDot,compareDots} from "../src/dot.ts";
test("persisted dot boundaries",()=>{
 assert.equal(formatDot(parseDot("18446744073709551615@site-B")),"18446744073709551615@site-B");
 assert.throws(()=>parseDot("18446744073709551616@site-B"));
 assert.throws(()=>parseDot("01@x"));
 assert.throws(()=>parseDot("1@x\n"));
 assert.ok(compareDots(parseDot("2@a"),parseDot("10@a"))<0);
 assert.ok(compareDots(parseDot("10@a"),parseDot("2@b"))<0);
});
