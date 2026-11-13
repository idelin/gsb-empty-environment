#include "netsec.hpp"

#include <map>
#include <stdexcept>
#include <utility>

namespace netsec {

const char* trap_message(Trap trap) {
  switch (trap) {
    case Trap::kNullFuncRef:
      return "call_indirect hit an uninitialized (null) funcref";
    case Trap::kIndirectTypeMismatch:
      return "call_indirect target has a mismatched signature";
  }
  return "unknown trap";
}

bool operator==(const FuncType& lhs, const FuncType& rhs) {
  return lhs.params == rhs.params && lhs.results == rhs.results;
}

bool operator!=(const FuncType& lhs, const FuncType& rhs) {
  return !(lhs == rhs);
}

void StateMachine::feed(int32_t state) { pending_.push_back(state); }

int32_t StateMachine::next_state() {
  if (pending_.empty()) return 0;
  int32_t state = pending_.front();
  pending_.erase(pending_.begin());
  return state;
}

void StateMachine::accept(int32_t state) {
  started_ = true;
  current_ = state;
}

std::vector<HostFunc> StateMachine::imports() {
  return {
      {"env", "fsm_next", {{}, {0x7F}},
       [this](const std::vector<int32_t>&) {
         return std::vector<int32_t>{next_state()};
       }},
      {"env", "fsm_accept", {{0x7F}, {}},
       [this](const std::vector<int32_t>& args) {
         accept(args.at(0));
         return std::vector<int32_t>{};
       }},
  };
}

namespace {

struct DecodeError : std::runtime_error {
  using std::runtime_error::runtime_error;
};

struct TrapException {
  Trap trap;
};

struct Reader {
  const std::vector<uint8_t>& bytes;
  size_t pos = 0;

  uint8_t u8() {
    if (pos >= bytes.size()) throw DecodeError("unexpected end of module");
    return bytes[pos++];
  }

  uint32_t uleb() {
    uint32_t result = 0;
    int shift = 0;
    for (;;) {
      uint8_t byte = u8();
      result |= uint32_t(byte & 0x7F) << shift;
      if (!(byte & 0x80)) return result;
      shift += 7;
      if (shift > 28) throw DecodeError("malformed LEB128");
    }
  }

  int32_t sleb() {
    int32_t result = 0;
    int shift = 0;
    uint8_t byte;
    do {
      byte = u8();
      result |= int32_t(byte & 0x7F) << shift;
      shift += 7;
    } while (byte & 0x80);
    if (shift < 32 && (byte & 0x40)) result |= -(1 << shift);
    return result;
  }

  std::string name() {
    uint32_t len = uleb();
    if (pos + len > bytes.size()) throw DecodeError("unexpected end of name");
    std::string out(reinterpret_cast<const char*>(&bytes[pos]), len);
    pos += len;
    return out;
  }
};

struct HostBinding {
  FuncType type;
  std::function<std::vector<int32_t>(const std::vector<int32_t>&)> fn;
};

}  // namespace

struct ModuleData {
  std::vector<FuncType> types;
  std::vector<HostBinding> host_funcs;  // imported functions come first
  std::vector<uint32_t> func_types;     // type index per defined function
  std::vector<std::vector<uint8_t>> codes;
  std::vector<int32_t> table;  // -1 = uninitialized (null) funcref
  std::map<std::string, uint32_t> exports;
  std::optional<uint32_t> start;
};

namespace {

const FuncType& type_of(const ModuleData& module, uint32_t funcidx) {
  if (funcidx < module.host_funcs.size()) {
    return module.host_funcs[funcidx].type;
  }
  return module.types[module.func_types[funcidx - module.host_funcs.size()]];
}

std::vector<int32_t> execute(const ModuleData& module, uint32_t funcidx,
                             const std::vector<int32_t>& args) {
  if (funcidx < module.host_funcs.size()) {
    return module.host_funcs[funcidx].fn(args);
  }
  uint32_t defined = funcidx - module.host_funcs.size();
  if (defined >= module.codes.size()) {
    throw DecodeError("function index out of range");
  }
  Reader reader{module.codes[defined], 0};
  std::vector<int32_t> locals = args;
  uint32_t decls = reader.uleb();
  for (uint32_t i = 0; i < decls; ++i) {
    uint32_t count = reader.uleb();
    reader.u8();  // value type, only i32 is used
    locals.insert(locals.end(), count, 0);
  }
  std::vector<int32_t> stack;
  for (;;) {
    uint8_t op = reader.u8();
    switch (op) {
      case 0x0B:    // end
      case 0x0F: {  // return
        const FuncType& type = type_of(module, funcidx);
        std::vector<int32_t> results(type.results.size());
        for (size_t i = type.results.size(); i-- > 0;) {
          if (stack.empty()) throw DecodeError("stack underflow on return");
          results[i] = stack.back();
          stack.pop_back();
        }
        return results;
      }
      case 0x20:  // local.get
        stack.push_back(locals.at(reader.uleb()));
        break;
      case 0x1A:  // drop
        if (stack.empty()) throw DecodeError("stack underflow on drop");
        stack.pop_back();
        break;
      case 0x41:  // i32.const
        stack.push_back(reader.sleb());
        break;
      case 0x6A: {  // i32.add
        int32_t rhs = stack.back();
        stack.pop_back();
        int32_t lhs = stack.back();
        stack.pop_back();
        stack.push_back(lhs + rhs);
        break;
      }
      case 0x10: {  // call
        uint32_t target = reader.uleb();
        const FuncType& type = type_of(module, target);
        std::vector<int32_t> call_args(type.params.size());
        for (size_t i = type.params.size(); i-- > 0;) {
          if (stack.empty()) throw DecodeError("stack underflow on call");
          call_args[i] = stack.back();
          stack.pop_back();
        }
        std::vector<int32_t> results = execute(module, target, call_args);
        stack.insert(stack.end(), results.begin(), results.end());
        break;
      }
      case 0x11: {  // call_indirect
        uint32_t typeidx = reader.uleb();
        uint32_t tableidx = reader.uleb();
        if (tableidx != 0) throw DecodeError("only table 0 is supported");
        if (stack.empty()) throw DecodeError("stack underflow on call_indirect");
        int32_t elem = stack.back();
        stack.pop_back();
        if (elem < 0 || size_t(elem) >= module.table.size() ||
            module.table[elem] < 0) {
          throw TrapException{Trap::kNullFuncRef};
        }
        uint32_t target = uint32_t(module.table[elem]);
        if (typeidx >= module.types.size() ||
            type_of(module, target) != module.types[typeidx]) {
          throw TrapException{Trap::kIndirectTypeMismatch};
        }
        const FuncType& type = module.types[typeidx];
        std::vector<int32_t> call_args(type.params.size());
        for (size_t i = type.params.size(); i-- > 0;) {
          if (stack.empty())
            throw DecodeError("stack underflow on call_indirect");
          call_args[i] = stack.back();
          stack.pop_back();
        }
        std::vector<int32_t> results = execute(module, target, call_args);
        stack.insert(stack.end(), results.begin(), results.end());
        break;
      }
      default:
        throw DecodeError("unsupported opcode");
    }
  }
}

ModuleData decode(const std::vector<uint8_t>& bytes,
                  const std::vector<HostFunc>& imports) {
  Reader reader{bytes, 0};
  const uint8_t kMagic[] = {0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00};
  for (uint8_t expected : kMagic) {
    if (reader.u8() != expected) throw DecodeError("bad magic or version");
  }

  ModuleData module;
  struct PendingImport {
    std::string module;
    std::string name;
    uint32_t typeidx;
  };
  std::vector<PendingImport> pending_imports;
  struct PendingElem {
    int32_t offset;
    std::vector<int32_t> funcs;
  };
  std::vector<PendingElem> pending_elems;

  while (reader.pos < bytes.size()) {
    uint8_t id = reader.u8();
    uint32_t size = reader.uleb();
    size_t end = reader.pos + size;
    if (end > bytes.size()) throw DecodeError("section size out of range");
    switch (id) {
      case 1: {  // type section
        uint32_t count = reader.uleb();
        for (uint32_t i = 0; i < count; ++i) {
          if (reader.u8() != 0x60) throw DecodeError("expected func type");
          FuncType type;
          uint32_t params = reader.uleb();
          for (uint32_t p = 0; p < params; ++p)
            type.params.push_back(reader.u8());
          uint32_t results = reader.uleb();
          for (uint32_t res = 0; res < results; ++res)
            type.results.push_back(reader.u8());
          module.types.push_back(std::move(type));
        }
        break;
      }
      case 2: {  // import section
        uint32_t count = reader.uleb();
        for (uint32_t i = 0; i < count; ++i) {
          PendingImport pending;
          pending.module = reader.name();
          pending.name = reader.name();
          if (reader.u8() != 0x00)
            throw DecodeError("only function imports are supported");
          pending.typeidx = reader.uleb();
          pending_imports.push_back(std::move(pending));
        }
        break;
      }
      case 3: {  // function section
        uint32_t count = reader.uleb();
        for (uint32_t i = 0; i < count; ++i)
          module.func_types.push_back(reader.uleb());
        break;
      }
      case 4: {  // table section
        uint32_t count = reader.uleb();
        if (count != 1) throw DecodeError("exactly one table is supported");
        if (reader.u8() != 0x70) throw DecodeError("only funcref tables");
        uint8_t flags = reader.u8();
        uint32_t min = reader.uleb();
        if (flags & 0x01) reader.uleb();  // max, ignored
        module.table.assign(min, -1);
        break;
      }
      case 7: {  // export section
        uint32_t count = reader.uleb();
        for (uint32_t i = 0; i < count; ++i) {
          std::string name = reader.name();
          uint8_t kind = reader.u8();
          uint32_t index = reader.uleb();
          if (kind == 0x00) module.exports[name] = index;
        }
        break;
      }
      case 8:  // start section
        module.start = reader.uleb();
        break;
      case 9: {  // element section
        uint32_t count = reader.uleb();
        for (uint32_t i = 0; i < count; ++i) {
          if (reader.uleb() != 0)
            throw DecodeError("only active table-0 segments are supported");
          PendingElem elem;
          if (reader.u8() != 0x41)
            throw DecodeError("expected i32.const offset");
          elem.offset = reader.sleb();
          if (reader.u8() != 0x0B) throw DecodeError("expected end of offset");
          uint32_t n = reader.uleb();
          for (uint32_t f = 0; f < n; ++f)
            elem.funcs.push_back(int32_t(reader.uleb()));
          pending_elems.push_back(std::move(elem));
        }
        break;
      }
      case 10: {  // code section
        uint32_t count = reader.uleb();
        for (uint32_t i = 0; i < count; ++i) {
          uint32_t body_size = reader.uleb();
          if (reader.pos + body_size > bytes.size())
            throw DecodeError("code body out of range");
          module.codes.emplace_back(bytes.begin() + reader.pos,
                                    bytes.begin() + reader.pos + body_size);
          reader.pos += body_size;
        }
        break;
      }
      default:  // unknown section: skip
        break;
    }
    reader.pos = end;
  }

  if (module.func_types.size() != module.codes.size())
    throw DecodeError("function/code section count mismatch");

  for (const PendingImport& pending : pending_imports) {
    const HostFunc* match = nullptr;
    for (const HostFunc& host : imports) {
      if (host.module == pending.module && host.name == pending.name) {
        match = &host;
        break;
      }
    }
    if (!match)
      throw DecodeError("unresolved import: " + pending.module + "." +
                        pending.name);
    if (pending.typeidx >= module.types.size() ||
        match->type != module.types[pending.typeidx])
      throw DecodeError("import signature mismatch: " + pending.module + "." +
                        pending.name);
    module.host_funcs.push_back({match->type, match->fn});
  }

  for (const PendingElem& elem : pending_elems) {
    if (elem.offset < 0 ||
        size_t(elem.offset) + elem.funcs.size() > module.table.size())
      throw DecodeError("element segment out of bounds");
    for (size_t i = 0; i < elem.funcs.size(); ++i)
      module.table[size_t(elem.offset) + i] = elem.funcs[i];
  }

  return module;
}

}  // namespace

Instantiation Engine::instantiate(const std::vector<uint8_t>& bytes,
                                  const std::vector<HostFunc>& imports) const {
  ModuleData module = decode(bytes, imports);
  if (module.start.has_value()) {
    try {
      execute(module, *module.start, {});
    } catch (const TrapException& trapped) {
      // The start function trapped: the whole instantiation fails and no
      // instance is handed out.
      return {std::nullopt, trapped.trap};
    }
  }
  auto data = std::make_shared<const ModuleData>(std::move(module));
  return {Instance(std::move(data)), std::nullopt};
}

bool Instance::has_export(const std::string& name) const {
  return data_->exports.count(name) != 0;
}

std::optional<int32_t> Instance::invoke(
    const std::string& name, const std::vector<int32_t>& args) const {
  auto it = data_->exports.find(name);
  if (it == data_->exports.end()) return std::nullopt;
  try {
    std::vector<int32_t> results = execute(*data_, it->second, args);
    if (results.empty()) return std::nullopt;
    return results.front();
  } catch (const TrapException&) {
    return std::nullopt;
  }
}

}  // namespace netsec
