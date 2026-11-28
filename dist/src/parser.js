import { annotate, apply, assign, boolLiteral, conditional, deref, intLiteral, lambda, letIn, list, project, record, remove, sequence, stringLiteral, tuple, unitLiteral, variable, } from "./ast.js";
import { boolType, constType, fnType, freshRowVariable, freshTypeVariable, intType, listType, recordType, refType, stringType, unitType, } from "./types.js";
export class ParseError extends Error {
    position;
    constructor(message, position) {
        super(`${message} at offset ${position}`);
        this.name = "ParseError";
        this.position = position;
    }
}
const MULTI_CHAR_PUNCTUATION = [
    "=>",
    "->",
    ":=",
    "==",
    "!=",
    "<=",
    ">=",
    "&&",
    "||",
    "::",
];
const SINGLE_CHAR_PUNCTUATION = new Set([
    "(",
    ")",
    "{",
    "}",
    "[",
    "]",
    ",",
    ";",
    ".",
    "|",
    "\\",
    ":",
    "+",
    "-",
    "*",
    "/",
    "<",
    ">",
    "=",
    "!",
    "?",
]);
function isIdentifierStart(character) {
    return /[A-Za-z_$]/.test(character);
}
function isIdentifierContinue(character) {
    return /[A-Za-z0-9_$']/.test(character);
}
export function tokenize(source) {
    const tokens = [];
    let index = 0;
    while (index < source.length) {
        const character = source[index];
        if (/\s/.test(character)) {
            index += 1;
            continue;
        }
        if (source.startsWith("//", index)) {
            const end = source.indexOf("\n", index + 2);
            index = end === -1 ? source.length : end + 1;
            continue;
        }
        if (source.startsWith("/*", index)) {
            const end = source.indexOf("*/", index + 2);
            if (end === -1)
                throw new ParseError("unterminated block comment", index);
            index = end + 2;
            continue;
        }
        if (character === '"') {
            let cursor = index + 1;
            let value = "";
            let closed = false;
            while (cursor < source.length) {
                const current = source[cursor];
                if (current === '"') {
                    closed = true;
                    cursor += 1;
                    break;
                }
                if (current === "\\") {
                    cursor += 1;
                    if (cursor >= source.length)
                        break;
                    const escaped = source[cursor];
                    switch (escaped) {
                        case "n":
                            value += "\n";
                            break;
                        case "r":
                            value += "\r";
                            break;
                        case "t":
                            value += "\t";
                            break;
                        case "\\":
                            value += "\\";
                            break;
                        case '"':
                            value += '"';
                            break;
                        default:
                            value += escaped;
                    }
                    cursor += 1;
                    continue;
                }
                value += current;
                cursor += 1;
            }
            if (!closed)
                throw new ParseError("unterminated string literal", index);
            tokens.push({
                kind: "string",
                value,
                position: index,
            });
            index = cursor;
            continue;
        }
        if (character === "'") {
            let cursor = index + 1;
            while (cursor < source.length &&
                /[A-Za-z0-9_$]/.test(source[cursor])) {
                cursor += 1;
            }
            if (cursor > index + 1 && source[cursor] === "'") {
                tokens.push({
                    kind: "string",
                    value: source.slice(index + 1, cursor),
                    position: index,
                });
                index = cursor + 1;
                continue;
            }
            if (cursor === index + 1) {
                throw new ParseError("invalid quote", index);
            }
            tokens.push({
                kind: "typevar",
                value: source.slice(index + 1, cursor),
                position: index,
            });
            index = cursor;
            continue;
        }
        if (/[0-9]/.test(character)) {
            let cursor = index + 1;
            while (cursor < source.length && /[0-9]/.test(source[cursor])) {
                cursor += 1;
            }
            tokens.push({
                kind: "number",
                value: source.slice(index, cursor),
                position: index,
            });
            index = cursor;
            continue;
        }
        if (isIdentifierStart(character)) {
            let cursor = index + 1;
            while (cursor < source.length &&
                isIdentifierContinue(source[cursor])) {
                cursor += 1;
            }
            tokens.push({
                kind: "ident",
                value: source.slice(index, cursor),
                position: index,
            });
            index = cursor;
            continue;
        }
        const multi = MULTI_CHAR_PUNCTUATION.find((punctuation) => source.startsWith(punctuation, index));
        if (multi !== undefined) {
            tokens.push({
                kind: "punct",
                value: multi,
                position: index,
            });
            index += multi.length;
            continue;
        }
        if (SINGLE_CHAR_PUNCTUATION.has(character)) {
            tokens.push({
                kind: "punct",
                value: character,
                position: index,
            });
            index += 1;
            continue;
        }
        throw new ParseError(`unexpected character '${character}'`, index);
    }
    tokens.push({ kind: "eof", value: "", position: source.length });
    return tokens;
}
const STOP_WORDS = new Set(["in", "then", "else", "rec"]);
class Parser {
    tokens;
    cursor = 0;
    namedTypeVariables = new Map();
    namedRowVariables = new Map();
    constructor(source) {
        this.tokens = tokenize(source);
    }
    parseExpressionSource() {
        const expression = this.parseExpression();
        this.expectEof();
        return expression;
    }
    parseTypeSource() {
        const type = this.parseType();
        this.expectEof();
        return type;
    }
    peek(offset = 0) {
        return this.tokens[Math.min(this.cursor + offset, this.tokens.length - 1)];
    }
    advance() {
        const token = this.peek();
        if (token.kind !== "eof")
            this.cursor += 1;
        return token;
    }
    matchPunctuation(value) {
        if (this.peek().kind === "punct" && this.peek().value === value) {
            this.advance();
            return true;
        }
        return false;
    }
    expectPunctuation(value) {
        const token = this.peek();
        if (token.kind !== "punct" || token.value !== value) {
            throw new ParseError(`expected '${value}'`, token.position);
        }
        return this.advance();
    }
    matchWord(value) {
        if (this.peek().kind === "ident" && this.peek().value === value) {
            this.advance();
            return true;
        }
        return false;
    }
    expectWord(value) {
        const token = this.peek();
        if (token.kind !== "ident" || token.value !== value) {
            throw new ParseError(`expected '${value}'`, token.position);
        }
        return this.advance();
    }
    expectIdentifier() {
        const token = this.peek();
        if (token.kind !== "ident" || STOP_WORDS.has(token.value)) {
            throw new ParseError("expected an identifier", token.position);
        }
        this.advance();
        return token.value;
    }
    expectEof() {
        const token = this.peek();
        if (token.kind !== "eof") {
            throw new ParseError("unexpected trailing input", token.position);
        }
    }
    parseExpression() {
        if (this.matchWord("let"))
            return this.parseLet();
        if (this.matchWord("fn"))
            return this.parseLambda(false);
        if (this.matchWord("fun"))
            return this.parseLambda(false);
        if (this.matchPunctuation("\\"))
            return this.parseLambda(true);
        const expression = this.parseSequence();
        if (this.matchPunctuation(":")) {
            return annotate(expression, this.parseType());
        }
        return expression;
    }
    parseLet() {
        const recursive = this.matchWord("rec");
        const name = this.expectIdentifier();
        this.expectPunctuation("=");
        const value = this.parseExpression();
        this.expectWord("in");
        const body = this.parseExpression();
        return letIn(name, value, body, recursive);
    }
    parseLambda(backslashAlreadyConsumed) {
        void backslashAlreadyConsumed;
        const parameters = [];
        while (this.peek().kind === "ident" &&
            !STOP_WORDS.has(this.peek().value) &&
            this.peek().value !== "fn" &&
            this.peek().value !== "fun") {
            if (this.peek(1).kind === "punct" &&
                (this.peek(1).value === "=>" || this.peek(1).value === "->")) {
                parameters.push(this.advance().value);
                break;
            }
            parameters.push(this.expectIdentifier());
        }
        if (parameters.length === 0) {
            throw new ParseError("lambda needs at least one parameter", this.peek().position);
        }
        if (!this.matchPunctuation("=>") &&
            !this.matchPunctuation("->")) {
            throw new ParseError("expected '=>' or '->'", this.peek().position);
        }
        let body = this.parseExpression();
        for (let index = parameters.length - 1; index >= 0; index -= 1) {
            body = lambda(parameters[index], body);
        }
        return body;
    }
    parseSequence() {
        const first = this.parseAssignment();
        if (this.matchPunctuation(";")) {
            return sequence(first, this.parseExpression());
        }
        return first;
    }
    parseAssignment() {
        const target = this.parseLogicalOr();
        if (this.matchPunctuation(":=")) {
            return assign(target, this.parseAssignment());
        }
        return target;
    }
    parseLogicalOr() {
        let left = this.parseLogicalAnd();
        while (this.matchPunctuation("||")) {
            left = apply(apply(variable("||"), left), this.parseLogicalAnd());
        }
        return left;
    }
    parseLogicalAnd() {
        let left = this.parseEquality();
        while (this.matchPunctuation("&&")) {
            left = apply(apply(variable("&&"), left), this.parseEquality());
        }
        return left;
    }
    parseEquality() {
        let left = this.parseComparison();
        while (this.peek().kind === "punct" &&
            (this.peek().value === "==" || this.peek().value === "!=")) {
            const operator = this.advance().value;
            left = apply(apply(variable(operator), left), this.parseComparison());
        }
        return left;
    }
    parseComparison() {
        let left = this.parseAdditive();
        while (this.peek().kind === "punct" &&
            ["<", "<=", ">", ">="].includes(this.peek().value)) {
            const operator = this.advance().value;
            left = apply(apply(variable(operator), left), this.parseAdditive());
        }
        return left;
    }
    parseAdditive() {
        let left = this.parseMultiplicative();
        while (this.peek().kind === "punct" &&
            (this.peek().value === "+" || this.peek().value === "-")) {
            const operator = this.advance().value;
            left = apply(apply(variable(operator), left), this.parseMultiplicative());
        }
        return left;
    }
    parseMultiplicative() {
        let left = this.parseCons();
        while (this.peek().kind === "punct" &&
            (this.peek().value === "*" || this.peek().value === "/")) {
            const operator = this.advance().value;
            left = apply(apply(variable(operator), left), this.parseCons());
        }
        return left;
    }
    parseCons() {
        const left = this.parseApplication();
        if (this.matchPunctuation("::")) {
            return apply(apply(variable("cons"), left), this.parseCons());
        }
        return left;
    }
    parseApplication() {
        let expression = this.parseUnary();
        while (true) {
            if (this.peek().kind === "punct" && this.peek().value === "(") {
                const argumentsList = this.parseCallArguments();
                for (const argument of argumentsList) {
                    expression = apply(expression, argument);
                }
                continue;
            }
            if (this.startsAtom(this.peek()) || this.isUnaryStart(this.peek())) {
                expression = apply(expression, this.parseUnary());
                continue;
            }
            break;
        }
        return expression;
    }
    parseCallArguments() {
        this.expectPunctuation("(");
        if (this.matchPunctuation(")"))
            return [unitLiteral()];
        const argumentsList = [];
        do {
            argumentsList.push(this.parseExpression());
        } while (this.matchPunctuation(","));
        this.expectPunctuation(")");
        return argumentsList;
    }
    isUnaryStart(token) {
        return token.kind === "punct" && (token.value === "!" || token.value === "-");
    }
    parseUnary() {
        if (this.matchPunctuation("!")) {
            return deref(this.parseUnary());
        }
        if (this.matchPunctuation("-")) {
            return apply(variable("negate"), this.parseUnary());
        }
        return this.parsePostfix();
    }
    parsePostfix() {
        let expression = this.parseAtom();
        while (this.matchPunctuation(".")) {
            expression = project(expression, this.expectIdentifier());
        }
        return expression;
    }
    startsAtom(token) {
        if (token.kind === "number" || token.kind === "string")
            return true;
        if (token.kind === "punct") {
            return token.value === "(" || token.value === "{" || token.value === "[";
        }
        if (token.kind !== "ident")
            return false;
        return !STOP_WORDS.has(token.value) && token.value !== "fun";
    }
    parseAtom() {
        const token = this.peek();
        if (token.kind === "number") {
            this.advance();
            return intLiteral(Number.parseInt(token.value, 10));
        }
        if (token.kind === "string") {
            this.advance();
            return stringLiteral(token.value);
        }
        if (token.kind === "ident") {
            if (token.value === "true") {
                this.advance();
                return boolLiteral(true);
            }
            if (token.value === "false") {
                this.advance();
                return boolLiteral(false);
            }
            if (token.value === "nil") {
                this.advance();
                return variable("nil");
            }
            if (token.value === "if") {
                this.advance();
                return this.parseIf();
            }
            if (token.value === "let") {
                this.advance();
                return this.parseLet();
            }
            if (token.value === "fn") {
                this.advance();
                return this.parseLambda(false);
            }
            if (STOP_WORDS.has(token.value)) {
                throw new ParseError(`unexpected keyword '${token.value}'`, token.position);
            }
            this.advance();
            return variable(token.value);
        }
        if (token.kind === "punct") {
            if (token.value === "(")
                return this.parseParenthesized();
            if (token.value === "{")
                return this.parseRecord();
            if (token.value === "[")
                return this.parseList();
        }
        throw new ParseError("expected an expression", token.position);
    }
    parseIf() {
        const condition = this.parseExpression();
        this.expectWord("then");
        const thenBranch = this.parseExpression();
        this.expectWord("else");
        const elseBranch = this.parseExpression();
        return conditional(condition, thenBranch, elseBranch);
    }
    parseParenthesized() {
        this.expectPunctuation("(");
        if (this.matchPunctuation(")"))
            return unitLiteral();
        const first = this.parseExpression();
        if (this.matchPunctuation(",")) {
            const items = [first];
            do {
                items.push(this.parseExpression());
            } while (this.matchPunctuation(","));
            this.expectPunctuation(")");
            return tuple(items);
        }
        this.expectPunctuation(")");
        return first;
    }
    parseList() {
        this.expectPunctuation("[");
        if (this.matchPunctuation("]"))
            return list([]);
        const items = [];
        do {
            items.push(this.parseExpression());
        } while (this.matchPunctuation(","));
        this.expectPunctuation("]");
        return list(items);
    }
    parseRecord() {
        this.expectPunctuation("{");
        if (this.matchPunctuation("}"))
            return record();
        if (this.matchPunctuation("|")) {
            const tail = this.parseExpression();
            this.expectPunctuation("}");
            return record([], tail);
        }
        const looksLikeFields = this.peek().kind === "ident" &&
            this.peek(1).kind === "punct" &&
            (this.peek(1).value === "=" || this.peek(1).value === ":");
        if (looksLikeFields) {
            const fields = [];
            while (true) {
                const label = this.expectIdentifier();
                if (!this.matchPunctuation("=") &&
                    !this.matchPunctuation(":")) {
                    throw new ParseError("expected '=' or ':' after record label", this.peek().position);
                }
                fields.push({ label, value: this.parseExpression() });
                if (!this.matchPunctuation(","))
                    break;
                if ((this.peek().kind === "punct" && this.peek().value === "}") ||
                    (this.peek().kind === "punct" && this.peek().value === "|")) {
                    break;
                }
            }
            let tail;
            if (this.matchPunctuation("|"))
                tail = this.parseExpression();
            this.expectPunctuation("}");
            return record(fields, tail);
        }
        let expression = this.parseExpression();
        const labels = [];
        while (this.matchPunctuation("\\")) {
            labels.push(this.expectIdentifier());
        }
        if (labels.length === 0) {
            throw new ParseError("expected a record field or a row deletion", this.peek().position);
        }
        this.expectPunctuation("}");
        for (const label of labels)
            expression = remove(expression, label);
        return expression;
    }
    parseType() {
        const left = this.parseTypeAtom();
        if (this.matchPunctuation("->")) {
            return fnType(left, this.parseType());
        }
        return left;
    }
    parseTypeAtom() {
        const token = this.peek();
        if (token.kind === "typevar") {
            this.advance();
            return this.namedTypeVariable(token.value);
        }
        if (token.kind === "ident") {
            this.advance();
            switch (token.value) {
                case "Int":
                case "int":
                    return intType;
                case "String":
                case "string":
                    return stringType;
                case "Bool":
                case "bool":
                    return boolType;
                case "Unit":
                case "unit":
                    return unitType;
                case "List": {
                    if (this.matchPunctuation("<") || this.matchPunctuation("[")) {
                        const element = this.parseType();
                        if (!this.matchPunctuation(">") && !this.matchPunctuation("]")) {
                            throw new ParseError("expected '>' after List type", this.peek().position);
                        }
                        return listType(element);
                    }
                    if (this.matchPunctuation("(")) {
                        const element = this.parseType();
                        this.expectPunctuation(")");
                        return listType(element);
                    }
                    throw new ParseError("List needs a type argument", this.peek().position);
                }
                case "Ref":
                case "ref": {
                    if (this.matchPunctuation("<") || this.matchPunctuation("[")) {
                        const element = this.parseType();
                        if (!this.matchPunctuation(">") && !this.matchPunctuation("]")) {
                            throw new ParseError("expected '>' after Ref type", this.peek().position);
                        }
                        return refType(element);
                    }
                    if (this.matchPunctuation("(")) {
                        const element = this.parseType();
                        this.expectPunctuation(")");
                        return refType(element);
                    }
                    throw new ParseError("Ref needs a type argument", this.peek().position);
                }
                default:
                    if (/^[A-Z]/.test(token.value))
                        return constType(token.value);
                    return this.namedTypeVariable(token.value);
            }
        }
        if (token.kind === "punct" && token.value === "(") {
            this.advance();
            if (this.matchPunctuation(")"))
                return unitType;
            const first = this.parseType();
            if (this.matchPunctuation(",")) {
                const items = [first];
                do {
                    items.push(this.parseType());
                } while (this.matchPunctuation(","));
                this.expectPunctuation(")");
                return { tag: "tuple", items };
            }
            this.expectPunctuation(")");
            return first;
        }
        if (token.kind === "punct" && token.value === "{") {
            return this.parseRecordType();
        }
        if (token.kind === "punct" && token.value === "[") {
            this.advance();
            const element = this.parseType();
            this.expectPunctuation("]");
            return listType(element);
        }
        throw new ParseError("expected a type", token.position);
    }
    parseRecordType() {
        this.expectPunctuation("{");
        if (this.matchPunctuation("}"))
            return recordType();
        if (this.matchPunctuation("|")) {
            const tail = this.parseRowTail();
            this.expectPunctuation("}");
            return recordType([], tail);
        }
        const fields = [];
        while (true) {
            const label = this.expectIdentifier();
            this.expectPunctuation(":");
            fields.push([label, this.parseType()]);
            if (!this.matchPunctuation(","))
                break;
            if (this.peek().kind === "punct" &&
                (this.peek().value === "}" || this.peek().value === "|")) {
                break;
            }
        }
        let tail;
        if (this.matchPunctuation("|"))
            tail = this.parseRowTail();
        this.expectPunctuation("}");
        return recordType(fields, tail);
    }
    parseRowTail() {
        const token = this.peek();
        if (token.kind === "typevar") {
            this.advance();
            return this.namedRowVariable(token.value);
        }
        if (token.kind === "ident") {
            this.advance();
            return this.namedRowVariable(token.value);
        }
        const type = this.parseTypeAtom();
        if (type.tag === "record")
            return type.row;
        throw new ParseError("expected a row variable", token.position);
    }
    namedTypeVariable(name) {
        let variable = this.namedTypeVariables.get(name);
        if (variable === undefined) {
            variable = freshTypeVariable();
            this.namedTypeVariables.set(name, variable);
        }
        return variable;
    }
    namedRowVariable(name) {
        let variable = this.namedRowVariables.get(name);
        if (variable === undefined) {
            variable = freshRowVariable();
            this.namedRowVariables.set(name, variable);
        }
        return variable;
    }
}
export function parse(source) {
    return new Parser(source).parseExpressionSource();
}
export const parseExpression = parse;
export const parseExpr = parse;
export function parseType(source) {
    return new Parser(source).parseTypeSource();
}
export const parseTypeText = parseType;
