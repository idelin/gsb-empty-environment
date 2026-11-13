#include "netsec.hpp"

#include <cstdio>
#include <vector>

namespace {

int g_failures = 0;

#define CHECK(cond)                                            \
  do {                                                         \
    if (!(cond)) {                                             \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,       \
                   __LINE__, #cond);                           \
      ++g_failures;                                            \
    }                                                          \
  } while (0)

// (module
//   (type () -> ())
//   (func 0: i32.const 0; call_indirect (type 0))   ;; start
//   (table 1 funcref)                                ;; slot 0 stays null
//   (start 0))
const std::vector<uint8_t> kNullFuncrefStart = {
    0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,  // magic + version
    0x01, 0x04, 0x01, 0x60, 0x00, 0x00,              // type: () -> ()
    0x03, 0x02, 0x01, 0x00,                          // func 0 : type 0
    0x04, 0x04, 0x01, 0x70, 0x00, 0x01,              // table 1 funcref
    0x08, 0x01, 0x00,                                // start = func 0
    0x0A, 0x09, 0x01, 0x07, 0x00, 0x41, 0x00, 0x11,  // code: i32.const 0;
    0x00, 0x00, 0x0B,                                //   call_indirect 0 0
};

// (module
//   (type 0: () -> ())  (type 1: (i32) -> ())
//   (func 0: i32.const 0; call_indirect (type 0))   ;; start
//   (func 1: type 1, empty body)
//   (table 1 funcref)
//   (elem (i32.const 0) func 1)                      ;; slot 0 = func 1
//   (start 0))                                       ;; type mismatch!
const std::vector<uint8_t> kTypeMismatchStart = {
    0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,  // magic + version
    0x01, 0x08, 0x02, 0x60, 0x00, 0x00, 0x60, 0x01,  // types: ()->(),
    0x7F, 0x00,                                      //   (i32)->()
    0x03, 0x03, 0x02, 0x00, 0x01,                    // funcs 0,1
    0x04, 0x04, 0x01, 0x70, 0x00, 0x01,              // table 1 funcref
    0x08, 0x01, 0x00,                                // start = func 0
    0x09, 0x07, 0x01, 0x00, 0x41, 0x00, 0x0B, 0x01,  // elem: table[0] =
    0x01,                                            //   func 1
    0x0A, 0x0C, 0x02, 0x07, 0x00, 0x41, 0x00, 0x11,  // code: call_indirect
    0x00, 0x00, 0x0B, 0x02, 0x00, 0x0B,              //   + empty body
};

// (module
//   (import "env" "fsm_next"   (func 0: () -> (i32)))
//   (import "env" "fsm_accept" (func 1: (i32) -> ()))
//   (func 2: call 0; call 1)                         ;; start
//   (func 3: i32.const 42)                           ;; exported "ping"
//   (start 2)
//   (export "ping" (func 3)))
const std::vector<uint8_t> kStateMachineStart = {
    0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,  // magic + version
    0x01, 0x10, 0x04, 0x60, 0x00, 0x01, 0x7F, 0x60,  // types: ()->i32,
    0x01, 0x7F, 0x00, 0x60, 0x00, 0x00, 0x60, 0x00,  //   (i32)->(), ()->(),
    0x01, 0x7F,                                      //   ()->i32
    0x02, 0x21, 0x02, 0x03, 0x65, 0x6E, 0x76, 0x08,  // import env.fsm_next
    0x66, 0x73, 0x6D, 0x5F, 0x6E, 0x65, 0x78, 0x74,  //   : type 0
    0x00, 0x00, 0x03, 0x65, 0x6E, 0x76, 0x0A, 0x66,  // import env.fsm_accept
    0x73, 0x6D, 0x5F, 0x61, 0x63, 0x63, 0x65, 0x70,  //   : type 1
    0x74, 0x00, 0x01,
    0x03, 0x03, 0x02, 0x02, 0x03,                    // funcs 2,3
    0x07, 0x08, 0x01, 0x04, 0x70, 0x69, 0x6E, 0x67,  // export "ping" =
    0x00, 0x03,                                      //   func 3
    0x08, 0x01, 0x02,                                // start = func 2
    0x0A, 0x0D, 0x02, 0x06, 0x00, 0x10, 0x00, 0x10,  // code: call 0; call 1
    0x01, 0x0B, 0x04, 0x00, 0x41, 0x2A, 0x0B,        //   + i32.const 42
};

void start_traps_on_null_funcref() {
  netsec::Engine engine;
  netsec::Instantiation result = engine.instantiate(kNullFuncrefStart, {});
  CHECK(!result.ok());
  CHECK(!result.instance.has_value());  // no instance is handed out
  CHECK(result.trap.has_value());
  CHECK(result.trap.value() == netsec::Trap::kNullFuncRef);
}

void start_traps_on_indirect_type_mismatch() {
  netsec::Engine engine;
  netsec::Instantiation result = engine.instantiate(kTypeMismatchStart, {});
  CHECK(!result.ok());
  CHECK(!result.instance.has_value());  // no instance is handed out
  CHECK(result.trap.has_value());
  CHECK(result.trap.value() == netsec::Trap::kIndirectTypeMismatch);
}

void start_returns_and_instance_is_usable() {
  netsec::Engine engine;
  netsec::StateMachine machine;
  machine.feed(7);  // runtime delivers state to the instance
  netsec::Instantiation result =
      engine.instantiate(kStateMachineStart, machine.imports());
  CHECK(result.ok());
  CHECK(!result.trap.has_value());
  // The start function pulled the runtime state and delivered it to the
  // imported C++ state machine.
  CHECK(machine.started());
  CHECK(machine.current_state() == 7);
  // The instance that returned normally is usable.
  CHECK(result.instance->has_export("ping"));
  std::optional<int32_t> pong = result.instance->invoke("ping");
  CHECK(pong.has_value());
  CHECK(pong.value() == 42);
}

}  // namespace

int main() {
  start_traps_on_null_funcref();
  start_traps_on_indirect_type_mismatch();
  start_returns_and_instance_is_usable();
  if (g_failures == 0) {
    std::printf("all netsec tests passed\n");
    return 0;
  }
  std::fprintf(stderr, "%d check(s) failed\n", g_failures);
  return 1;
}
