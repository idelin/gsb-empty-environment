cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(netsec
    netsec.cpp
    netsec/wire.cpp
    netsec/certificate.cpp
    netsec/trust_store.cpp
    netsec/policy.cpp
)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)

enable_testing()

add_executable(netsec_tests
    tests/test_constraints.cpp
    tests/cert_builder.cpp
)
target_link_libraries(netsec_tests PRIVATE netsec)
target_include_directories(netsec_tests PRIVATE
    "${CMAKE_CURRENT_SOURCE_DIR}/tests")
target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
add_test(NAME netsec_tests COMMAND netsec_tests)
