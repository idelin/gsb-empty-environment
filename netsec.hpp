#pragma once

#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace netsec {

// Traps raised while running the start function.
enum class Trap {
  kNullFuncRef,           // call_indirect hit an uninitialized (null) funcref
  kIndirectTypeMismatch,  // call_indirect target has a mismatched signature
};

const char* trap_message(Trap trap);

struct FuncType {
  std::vector<uint8_t> params;   // 0x7F = i32
  std::vector<uint8_t> results;  // 0x7F = i32
};

bool operator==(const FuncType& lhs, const FuncType& rhs);
bool operator!=(const FuncType& lhs, const FuncType& rhs);

// A host function import, resolved by (module, name).
struct HostFunc {
  std::string module;
  std::string name;
  FuncType type;
  std::function<std::vector<int32_t>(const std::vector<int32_t>&)> fn;
};

// Importable C++ state machine. The runtime feeds it state via feed(); the
// wasm instance pulls the pending state through the "env.fsm_next" import and
// delivers it back to the state machine through "env.fsm_accept".
class StateMachine {
public:
  void feed(int32_t state);  // runtime hands state to deliver to the instance

  int32_t next_state();   // wasm -> host: hand out the pending state
  void accept(int32_t state);  // wasm -> host: apply the state transition

  bool started() const { return started_; }
  int32_t current_state() const { return current_; }

  std::vector<HostFunc> imports();

private:
  std::vector<int32_t> pending_;
  bool started_ = false;
  int32_t current_ = 0;
};

struct ModuleData;

// A successfully instantiated module. Only handed out when instantiation
// completed, i.e. the start function (if any) returned normally.
class Instance {
public:
  bool has_export(const std::string& name) const;
  std::optional<int32_t> invoke(const std::string& name,
                                const std::vector<int32_t>& args = {}) const;

private:
  friend class Engine;
  explicit Instance(std::shared_ptr<const ModuleData> data)
      : data_(std::move(data)) {}
  std::shared_ptr<const ModuleData> data_;
};

struct Instantiation {
  std::optional<Instance> instance;  // empty when the start function trapped
  std::optional<Trap> trap;          // set when the start function trapped
  bool ok() const { return instance.has_value(); }
};

class Engine {
public:
  // Instantiates the module. If the start function traps (e.g. by calling a
  // null funcref or an illegally typed indirect function), the whole
  // instantiation fails and no instance is returned.
  // Throws std::runtime_error on malformed modules or unresolved imports.
  Instantiation instantiate(const std::vector<uint8_t>& bytes,
                            const std::vector<HostFunc>& imports) const;
};

}  // namespace netsec
