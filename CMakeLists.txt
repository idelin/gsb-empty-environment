cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

option(NETSEC_SANITIZE "Build with address/undefined sanitizers" OFF)
option(NETSEC_TSAN "Build with thread sanitizer" OFF)

add_executable(lease_tests tests/lease_tests.cpp)
target_link_libraries(lease_tests PRIVATE netsec)

if(NETSEC_SANITIZE)
    target_compile_options(netsec PUBLIC -fsanitize=address,undefined
        -fno-omit-frame-pointer -g)
    target_link_options(netsec PUBLIC -fsanitize=address,undefined)
endif()

if(NETSEC_TSAN)
    target_compile_options(netsec PUBLIC -fsanitize=thread
        -fno-omit-frame-pointer -g)
    target_link_options(netsec PUBLIC -fsanitize=thread)
endif()

add_test(NAME lease_tests COMMAND lease_tests)
