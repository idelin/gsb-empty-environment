cmake_minimum_required(VERSION 3.16)
project(cet_shadow_guard CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(CET_BUILD_TESTS "Build the CET state-machine tests" ON)

add_library(cet_shadow_guard STATIC src/cet.cpp)
add_library(cet::shadow_guard ALIAS cet_shadow_guard)
target_compile_features(cet_shadow_guard PUBLIC cxx_std_17)

target_include_directories(
    cet_shadow_guard
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>)

target_compile_options(
    cet_shadow_guard
    PRIVATE
        -Wall
        -Wextra
        -Wconversion
        -Werror)

if(CET_BUILD_TESTS)
    enable_testing()
    find_package(Threads REQUIRED)

    add_executable(cet_state_tests tests/cet_tests.cpp)
    target_link_libraries(cet_state_tests PRIVATE cet_shadow_guard Threads::Threads)
    target_compile_options(
        cet_state_tests
        PRIVATE
            -Wall
            -Wextra
            -Wconversion
            -Werror)

    add_test(NAME cet_state_tests COMMAND cet_state_tests)
endif()
