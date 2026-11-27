cmake_minimum_required(VERSION 3.16)
project(w27_b6e3e0887c CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Header-only, zero-dependency FFI boundary lifetime/aliasing verifier.
add_library(ffi_safety INTERFACE)
target_include_directories(ffi_safety INTERFACE
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>)
target_compile_features(ffi_safety INTERFACE cxx_std_17)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang|AppleClang")
  add_library(ffi_safety_warnings INTERFACE)
  target_compile_options(ffi_safety_warnings INTERFACE
    -Wall
    -Wextra
    -Wconversion
    -Wsign-conversion
    -Wshadow
    -pedantic
    -Werror)
endif()

# Standalone include-what-you-need check for the public header.
add_executable(ffi_safety_header_check tests/header_check.cpp)
target_link_libraries(ffi_safety_header_check PRIVATE ffi_safety ffi_safety_warnings)

# Deterministic boundary and re-entrancy adversarial tests.
enable_testing()
add_executable(ffi_safety_tests tests/ffi_safety_tests.cpp)
target_link_libraries(ffi_safety_tests PRIVATE ffi_safety ffi_safety_warnings)
add_test(NAME ffi_safety_tests COMMAND ffi_safety_tests)
