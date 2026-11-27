cmake_minimum_required(VERSION 3.16)
project(landlock_nested_policy LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(landlock_nested_policy INTERFACE)
add_library(landlock::nested_policy ALIAS landlock_nested_policy)
target_include_directories(
    landlock_nested_policy
    INTERFACE
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
)
target_compile_features(landlock_nested_policy INTERFACE cxx_std_17)

option(LANDLOCK_NESTED_POLICY_BUILD_TESTS "Build the standalone test suite" ON)

if(LANDLOCK_NESTED_POLICY_BUILD_TESTS)
    enable_testing()
    add_executable(landlock_nested_policy_tests
        tests/test_landlock_nested_policy.cpp
    )
    target_link_libraries(landlock_nested_policy_tests
        PRIVATE landlock::nested_policy
    )
    if(MSVC)
        target_compile_options(landlock_nested_policy_tests PRIVATE /W4 /WX)
    else()
        target_compile_options(landlock_nested_policy_tests PRIVATE
            -Wall -Wextra -Wconversion -Werror -pedantic
        )
    endif()
    add_test(NAME landlock_nested_policy_tests
        COMMAND landlock_nested_policy_tests
    )
endif()

include(GNUInstallDirs)
install(FILES include/landlock_nested_policy.hpp
    DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}
)
