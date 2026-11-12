cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

enable_testing()

add_executable(netsec_test tests/netsec_test.cpp)
target_link_libraries(netsec_test PRIVATE netsec)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
    target_compile_options(netsec_test PRIVATE -Wall -Wextra -Wpedantic -Werror)
endif()

add_test(NAME netsec_test COMMAND netsec_test)
