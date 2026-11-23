cmake_minimum_required(VERSION 3.16)
project(ps_cf0a009ca3 CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(gsb_reassembly INTERFACE)
target_include_directories(
    gsb_reassembly
    INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_compile_features(gsb_reassembly INTERFACE cxx_std_20)

add_executable(
    test_reassembly
    tests/main.cpp
    tests/test_cases_a.cpp
    tests/test_cases_b.cpp
    tests/test_cases_c.cpp
    tests/test_oracle.cpp)
target_link_libraries(test_reassembly PRIVATE gsb_reassembly)
target_include_directories(
    test_reassembly PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/tests)

include(CTest)
add_test(NAME reassembly COMMAND test_reassembly)

# C++20 named modules are built through scripts/build_module.sh because the
# available Makefile generator does not perform module dependency scanning.
add_custom_target(
    module_smoke
    COMMAND ${CMAKE_COMMAND} -E env
            ${CMAKE_CURRENT_SOURCE_DIR}/scripts/build_module.sh
            ${CMAKE_BINARY_DIR}/module
    WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}
    VERBATIM)
