'use strict';

const { Lexer, MODE } = require('./lexer');
const { IncrementalLexer } = require('./incremental');

module.exports = { Lexer, IncrementalLexer, MODE };
