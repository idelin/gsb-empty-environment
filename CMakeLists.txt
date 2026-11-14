cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(NETSEC_SANITIZE "Build the library and tests with ASan/UBSan" OFF)

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

if(NETSEC_SANITIZE)
    target_compile_options(netsec PRIVATE -fsanitize=address,undefined
        -fno-omit-frame-pointer)
    target_link_options(netsec INTERFACE -fsanitize=address,undefined)
endif()

add_executable(session_test tests/session_test.cpp)
target_link_libraries(session_test PRIVATE netsec)

enable_testing()
add_test(NAME session_security COMMAND session_test)
