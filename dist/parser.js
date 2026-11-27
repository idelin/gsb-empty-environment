import { JsonPathParseError } from "./errors.js";
import { tokenize } from "./lexer.js";
const PRECEDENCE = {
    or: 1,
    and: 2,
    eq: 3,
    ne: 3,
    lt: 3,
    le: 3,
    gt: 3,
    ge: 3,
    plus: 4,
    minus: 4,
    star: 5,
    slash: 5,
    percent: 5,
};
const BINARY_TEXT = {
    or: "||",
    and: "&&",
    eq: "==",
    ne: "!=",
    lt: "<",
    le: "<=",
    gt: ">",
    ge: ">=",
    plus: "+",
    minus: "-",
    star: "*",
    slash: "/",
    percent: "%",
};
function lower(token) {
    return token.value.toLocaleLowerCase("en-US");
}
export class JsonPathParser {
    tokens;
    source;
    cursor = 0;
    constructor(source) {
        this.source = source;
        this.tokens = tokenize(source);
    }
    parse(options = {}) {
        let mode = options.defaultMode ?? "lax";
        let modeDeclared = false;
        if (this.peek().type === "identifier") {
            const candidate = lower(this.peek());
            if (candidate === "lax" || candidate === "strict") {
                mode = candidate;
                modeDeclared = true;
                this.advance();
            }
        }
        const rootToken = this.expect("dollar");
        const root = {
            kind: "path",
            origin: "$",
            steps: this.parsePathSteps(),
            offset: rootToken.offset,
        };
        if (this.peek().type !== "eof") {
            throw this.error(`unexpected token '${this.peek().value}'`);
        }
        return {
            kind: "jsonpath",
            mode,
            modeDeclared,
            root,
            source: this.source,
        };
    }
    parsePathExpression(originToken) {
        const origin = originToken.type;
        return {
            kind: "path",
            origin,
            steps: this.parsePathSteps(),
            offset: originToken.offset,
        };
    }
    parsePathSteps() {
        const steps = [];
        for (;;) {
            const token = this.peek();
            if (token.type === "dot") {
                steps.push(this.parseDotStep());
                continue;
            }
            if (token.type === "dotStar") {
                this.advance();
                steps.push({ kind: "memberWildcard", offset: token.offset });
                continue;
            }
            if (token.type === "recursive") {
                this.advance();
                steps.push({ kind: "recursive", offset: token.offset });
                continue;
            }
            if (token.type === "lbracket") {
                steps.push(this.parseArrayStep());
                continue;
            }
            if (token.type === "question") {
                this.advance();
                this.expect("lparen");
                const predicate = this.parseExpression(0);
                this.expect("rparen");
                steps.push({ kind: "filter", predicate, offset: token.offset });
                continue;
            }
            break;
        }
        return steps;
    }
    parseDotStep() {
        const dot = this.expect("dot");
        if (this.peek().type === "star") {
            this.advance();
            return { kind: "memberWildcard", offset: dot.offset };
        }
        const nameToken = this.peek();
        if (nameToken.type === "identifier") {
            this.advance();
            if (this.peek().type === "lparen") {
                return this.parseMethod(nameToken);
            }
            return {
                kind: "member",
                key: nameToken.value,
                offset: nameToken.offset,
            };
        }
        if (nameToken.type === "string") {
            this.advance();
            return {
                kind: "member",
                key: nameToken.value,
                offset: nameToken.offset,
            };
        }
        throw this.error("expected a member name after '.'");
    }
    parseMethod(nameToken) {
        this.expect("lparen");
        const args = [];
        if (this.peek().type !== "rparen") {
            for (;;) {
                args.push(this.parseLiteralArgument());
                if (this.peek().type !== "comma") {
                    break;
                }
                this.advance();
            }
        }
        this.expect("rparen");
        return {
            kind: "method",
            name: lower(nameToken),
            args,
            offset: nameToken.offset,
        };
    }
    parseLiteralArgument() {
        const token = this.peek();
        if (token.type === "string") {
            this.advance();
            return { kind: "literal", value: token.value, offset: token.offset };
        }
        if (token.type === "number") {
            this.advance();
            return {
                kind: "literal",
                value: Number(token.value),
                offset: token.offset,
            };
        }
        if (token.type === "identifier") {
            const value = lower(token);
            if (value === "true" || value === "false" || value === "null") {
                this.advance();
                return {
                    kind: "literal",
                    value: value === "null" ? null : value === "true",
                    offset: token.offset,
                };
            }
        }
        throw this.error("method arguments must be literals");
    }
    parseArrayStep() {
        const start = this.expect("lbracket");
        if (this.peek().type === "star") {
            this.advance();
            this.expect("rbracket");
            return {
                kind: "arrayAccess",
                selectors: [],
                wildcard: true,
                offset: start.offset,
            };
        }
        const selectors = [];
        for (;;) {
            const fromExpression = this.parseExpression(0);
            const from = this.toIndexExpression(fromExpression);
            if (this.peek().type === "identifier" &&
                lower(this.peek()) === "to") {
                this.advance();
                const to = this.toIndexExpression(this.parseExpression(0));
                selectors.push({ kind: "range", from, to });
            }
            else {
                selectors.push({ kind: "index", expression: from });
            }
            if (this.peek().type !== "comma") {
                break;
            }
            this.advance();
        }
        this.expect("rbracket");
        return {
            kind: "arrayAccess",
            selectors,
            wildcard: false,
            offset: start.offset,
        };
    }
    toIndexExpression(expression) {
        switch (expression.kind) {
            case "literal":
                if (typeof expression.value === "number" &&
                    Number.isInteger(expression.value)) {
                    return { kind: "integer", value: expression.value };
                }
                break;
            case "last":
                return { kind: "last" };
            case "unary":
                if (expression.operator === "+" || expression.operator === "-") {
                    return {
                        kind: "unary",
                        operator: expression.operator,
                        operand: this.toIndexExpression(expression.operand),
                    };
                }
                break;
            case "binary":
                if (expression.operator === "+" ||
                    expression.operator === "-" ||
                    expression.operator === "*" ||
                    expression.operator === "/" ||
                    expression.operator === "%") {
                    return {
                        kind: "binary",
                        operator: expression.operator,
                        left: this.toIndexExpression(expression.left),
                        right: this.toIndexExpression(expression.right),
                    };
                }
                break;
            default:
                break;
        }
        throw new JsonPathParseError("array index must be an integer expression", this.source, expression.offset);
    }
    parseExpression(minPrecedence) {
        let left = this.parseUnary();
        for (;;) {
            const token = this.peek();
            if (token.type === "identifier" &&
                lower(token) === "like_regex" &&
                3 >= minPrecedence) {
                this.advance();
                const pattern = this.parseExpression(4);
                if (pattern.kind !== "literal" || typeof pattern.value !== "string") {
                    throw new JsonPathParseError("like_regex pattern must be a string literal", this.source, pattern.offset);
                }
                let flags = "";
                if (this.peek().type === "identifier" &&
                    lower(this.peek()) === "flag") {
                    this.advance();
                    const flagToken = this.expect("string");
                    flags = flagToken.value;
                }
                left = {
                    kind: "likeRegex",
                    left,
                    pattern: pattern.value,
                    flags,
                    offset: token.offset,
                };
                continue;
            }
            const precedence = PRECEDENCE[token.type];
            if (precedence === undefined || precedence < minPrecedence) {
                break;
            }
            this.advance();
            const right = this.parseExpression(precedence + 1);
            const operator = BINARY_TEXT[token.type];
            left = {
                kind: "binary",
                operator,
                left,
                right,
                offset: token.offset,
            };
        }
        return left;
    }
    parseUnary() {
        const token = this.peek();
        if (token.type === "not" ||
            token.type === "plus" ||
            token.type === "minus") {
            this.advance();
            return {
                kind: "unary",
                operator: token.type === "not" ? "!" : token.type === "plus" ? "+" : "-",
                operand: this.parseUnary(),
                offset: token.offset,
            };
        }
        return this.parsePrimary();
    }
    parsePrimary() {
        const token = this.peek();
        if (token.type === "dollar" || token.type === "at") {
            this.advance();
            return this.parsePathExpression(token);
        }
        if (token.type === "number") {
            this.advance();
            return {
                kind: "literal",
                value: Number(token.value),
                offset: token.offset,
            };
        }
        if (token.type === "string") {
            this.advance();
            return { kind: "literal", value: token.value, offset: token.offset };
        }
        if (token.type === "lparen") {
            this.advance();
            const expression = this.parseExpression(0);
            this.expect("rparen");
            return expression;
        }
        if (token.type === "identifier") {
            const value = lower(token);
            if (value === "true" || value === "false" || value === "null") {
                this.advance();
                return {
                    kind: "literal",
                    value: value === "null" ? null : value === "true",
                    offset: token.offset,
                };
            }
            if (value === "last") {
                this.advance();
                return { kind: "last", offset: token.offset };
            }
            if (value === "exists") {
                this.advance();
                this.expect("lparen");
                const pathToken = this.peek();
                if (pathToken.type !== "dollar" && pathToken.type !== "at") {
                    throw this.error("exists() expects a path expression");
                }
                this.advance();
                const path = this.parsePathExpression(pathToken);
                this.expect("rparen");
                return {
                    kind: "exists",
                    path,
                    offset: token.offset,
                };
            }
        }
        throw this.error(`unexpected token '${token.value}'`);
    }
    peek() {
        return this.tokens[this.cursor];
    }
    advance() {
        const token = this.tokens[this.cursor];
        if (token.type !== "eof") {
            this.cursor += 1;
        }
        return token;
    }
    expect(type) {
        const token = this.peek();
        if (token.type !== type) {
            throw this.error(`expected '${type}' but found '${token.value || "end"}'`);
        }
        this.cursor += 1;
        return token;
    }
    error(message) {
        return new JsonPathParseError(message, this.source, this.peek().offset);
    }
}
export function parseJsonPath(source, options = {}) {
    if (typeof source !== "string") {
        throw new TypeError("jsonpath must be a string");
    }
    return new JsonPathParser(source).parse(options);
}
//# sourceMappingURL=parser.js.map