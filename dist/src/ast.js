export function intLiteral(value) {
    return { tag: "lit", kind: "Int", value };
}
export function stringLiteral(value) {
    return { tag: "lit", kind: "String", value };
}
export function boolLiteral(value) {
    return { tag: "lit", kind: "Bool", value };
}
export function unitLiteral() {
    return { tag: "lit", kind: "Unit", value: null };
}
export const int = intLiteral;
export const integer = intLiteral;
export const num = intLiteral;
export const string = stringLiteral;
export const str = stringLiteral;
export const bool = boolLiteral;
export const boolean = boolLiteral;
export const unit = unitLiteral;
export const unitValue = unitLiteral;
export function variable(name) {
    return { tag: "var", name };
}
export const varExpr = variable;
export const variableExpr = variable;
export const v = variable;
export const var_ = variable;
export function lambda(param, body) {
    return { tag: "lambda", param, body };
}
export const fn = lambda;
export const lam = lambda;
export const lambdaExpr = lambda;
export function apply(fnExpr, arg) {
    return { tag: "app", fn: fnExpr, arg };
}
export const app = apply;
export const ap = apply;
export const application = apply;
export function applyMany(fnExpr, args) {
    return args.reduce((current, arg) => apply(current, arg), fnExpr);
}
export function letIn(name, value, body, recursive = false) {
    return recursive
        ? { tag: "let", name, value, body, recursive: true }
        : { tag: "let", name, value, body };
}
export const let_ = letIn;
export const letExpr = letIn;
export const letBinding = letIn;
function normalizeFields(fields) {
    if (Array.isArray(fields)) {
        return fields.map((entry) => {
            if (Array.isArray(entry)) {
                return { label: entry[0], value: entry[1] };
            }
            return entry;
        });
    }
    return Object.entries(fields).map(([label, value]) => ({ label, value }));
}
export function record(fields = {}, tail) {
    const normalized = normalizeFields(fields);
    return tail === undefined
        ? { tag: "record", fields: normalized }
        : { tag: "record", fields: normalized, tail };
}
export const recordExpr = record;
export const recordConstructor = record;
export function extend(label, value, tail) {
    return record([{ label, value }], tail);
}
export const extendExpr = extend;
export const recordExtension = extend;
export function project(recordExpr, label) {
    return { tag: "project", record: recordExpr, label };
}
export const projection = project;
export const fieldProjection = project;
export function remove(recordExpr, label) {
    return { tag: "remove", record: recordExpr, label };
}
export const deleteField = remove;
export const removeField = remove;
export const fieldRemoval = remove;
export function ref(value) {
    return { tag: "ref", value };
}
export const refExpr = ref;
export const newRef = ref;
export function deref(value) {
    return { tag: "deref", value };
}
export const dereference = deref;
export const derefExpr = deref;
export function assign(target, value) {
    return { tag: "assign", target, value };
}
export const assignment = assign;
export const assignExpr = assign;
export function sequence(first, rest) {
    return { tag: "seq", first, rest };
}
export const seq = sequence;
export const sequenceExpr = sequence;
export function tuple(items) {
    return { tag: "tuple", items };
}
export const tupleExpr = tuple;
export function list(items) {
    return { tag: "list", items };
}
export const listExpr = list;
export function conditional(condition, thenBranch, elseBranch) {
    return { tag: "if", condition, thenBranch, elseBranch };
}
export const ifThenElse = conditional;
export const ifExpr = conditional;
export function annotate(expr, type) {
    return { tag: "annotate", expr, type };
}
export const annotation = annotate;
export const typeAnnotation = annotate;
