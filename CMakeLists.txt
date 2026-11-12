cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

add_executable(lease_test tests/lease_test.cpp)
target_link_libraries(lease_test PRIVATE netsec)
target_include_directories(lease_test PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/tests")

add_executable(concurrency_stress_test tests/concurrency_stress_test.cpp)
target_link_libraries(concurrency_stress_test PRIVATE netsec)
target_include_directories(concurrency_stress_test
                           PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/tests")

target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
target_compile_options(lease_test PRIVATE -Wall -Wextra -Wpedantic)
target_compile_options(concurrency_stress_test PRIVATE -Wall -Wextra -Wpedantic)
enable_testing()
add_test(NAME lease_test COMMAND lease_test)
add_test(NAME concurrency_stress_test COMMAND concurrency_stress_test)
