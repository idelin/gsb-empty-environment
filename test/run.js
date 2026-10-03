'use strict';
// Runs every *.test.js in this directory using Node.js built-ins only.
const fs = require('fs');
const path = require('path');

const files = fs
  .readdirSync(__dirname)
  .filter((f) => f.endsWith('.test.js'))
  .sort();

for (const f of files) {
  require(path.join(__dirname, f));
}
