if(NOT DEFINED CXX OR NOT DEFINED SRC OR NOT DEFINED INC OR NOT DEFINED OUT)
  message(FATAL_ERROR "check_barrier_asm.cmake requires CXX, SRC, INC, and OUT")
endif()

execute_process(
  COMMAND "${CXX}" -std=c++20 -O2 -S "-I${INC}" "${SRC}" -o "${OUT}"
  RESULT_VARIABLE compile_result
  OUTPUT_VARIABLE compile_stdout
  ERROR_VARIABLE compile_stderr)

if(NOT compile_result EQUAL 0)
  message(FATAL_ERROR
    "barrier probe compilation failed:\n${compile_stdout}\n${compile_stderr}")
endif()

file(READ "${OUT}" asm_text)
string(TOLOWER "${asm_text}" asm_text)
string(TOLOWER "${PROC}" proc)

if(proc MATCHES "x86_64|amd64|x86|i[3-6]86")
  set(expected "lfence")
  set(reason "x86 speculation barrier must emit LFENCE")
elseif(proc MATCHES "aarch64|arm64")
  set(expected "csdb")
  set(reason "AArch64 speculation barrier must emit CSDB")
elseif(proc MATCHES "arm")
  set(expected "csdb|isb")
  set(reason "ARM speculation barrier must emit CSDB or ISB")
elseif(proc MATCHES "riscv")
  set(expected "fence")
  set(reason "RISC-V speculation barrier must emit FENCE")
elseif(proc MATCHES "ppc|powerpc")
  set(expected "isync")
  set(reason "POWER speculation barrier must emit ISYNC")
elseif(proc MATCHES "s390")
  set(expected "bcr")
  set(reason "s390 speculation barrier must emit BCR")
else()
  message(STATUS "No architecture-specific assertion for processor '${PROC}'")
  return()
endif()

if(NOT asm_text MATCHES "${expected}")
  message(FATAL_ERROR
    "${reason}; expected /${expected}/ in ${OUT}, but it was not found")
endif()

message(STATUS "Barrier instruction check passed for '${PROC}' (/${expected}/)")
