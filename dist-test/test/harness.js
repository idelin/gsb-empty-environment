"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.report = exports.test = void 0;
let count = 0;
let failures = 0;
function test(name, fn) {
    count += 1;
    try {
        fn();
        process.stdout.write(`ok ${count} - ${name}\n`);
    }
    catch (err) {
        failures += 1;
        process.stdout.write(`not ok ${count} - ${name}\n`);
        console.error(err);
    }
}
exports.test = test;
function report() {
    process.stdout.write(`# ${count} tests, ${failures} failed\n`);
    if (failures > 0) {
        process.exit(1);
    }
}
exports.report = report;
