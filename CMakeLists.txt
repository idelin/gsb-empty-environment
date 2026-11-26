cmake_minimum_required(VERSION 3.16)
project(w27_9eb9a0de3a CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(ccm INTERFACE)
target_include_directories(ccm INTERFACE
    ${CMAKE_CURRENT_SOURCE_DIR}/include)

target_compile_options(ccm INTERFACE
    -Wall
    -Wextra
    -Wconversion
    -Wsign-conversion
    -Werror
    -pedantic)

enable_testing()

add_executable(ccm_demo demo/main.cpp)
target_link_libraries(ccm_demo PRIVATE ccm)

set(CCM_TESTS auditor marshal adversary)
foreach(test_name IN LISTS CCM_TESTS)
    add_executable(test_${test_name} tests/test_${test_name}.cpp)
    target_include_directories(test_${test_name} PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/tests)
    target_link_libraries(test_${test_name} PRIVATE ccm)
    add_test(NAME ${test_name} COMMAND test_${test_name})
endforeach()
