cmake_minimum_required(VERSION 3.16)
project(bounded_queue CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Threads REQUIRED)

add_executable(bounded_queue_tests tests/bounded_queue_tests.cpp)
target_include_directories(bounded_queue_tests PRIVATE include)
target_link_libraries(bounded_queue_tests PRIVATE Threads::Threads)
target_compile_options(bounded_queue_tests PRIVATE -Wall -Wextra)

enable_testing()
add_test(NAME bounded_queue_tests COMMAND bounded_queue_tests)
