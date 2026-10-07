cmake_minimum_required(VERSION 3.16)
project(resource_executor CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Threads REQUIRED)

add_executable(resource_tests tests.cpp)
target_compile_options(resource_tests PRIVATE -Wall -Wextra)
target_link_libraries(resource_tests PRIVATE Threads::Threads)

enable_testing()
add_test(NAME resource_tests COMMAND resource_tests)
