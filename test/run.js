"use strict";
const { run } = require("./harness");
require("./hash.test");
require("./evaluation.test");
require("./snapshots.test");
require("./dependencies.test");
require("./cache.test");
run();
