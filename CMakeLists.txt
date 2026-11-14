cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(BUILD_SHARED_LIBS "Build netsec as a shared library" ON)

add_library(netsec netsec.cpp netsec.hpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
set_target_properties(netsec PROPERTIES WINDOWS_EXPORT_ALL_SYMBOLS ON)

find_package(Threads REQUIRED)
target_link_libraries(netsec PUBLIC Threads::Threads)

if (CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
    target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

enable_testing()
add_executable(block_transfer_test tests/block_transfer_test.cpp)
target_link_libraries(block_transfer_test PRIVATE netsec)
if (CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
    target_compile_options(block_transfer_test PRIVATE -Wall -Wextra -Wpedantic)
endif()
add_test(NAME block_transfer COMMAND block_transfer_test)
