cmake_minimum_required(VERSION 3.16)
project(ringlog CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(ringlog src/ringlog.cc)
target_include_directories(ringlog PUBLIC include)
target_compile_options(ringlog PRIVATE -Wall -Wextra)

add_executable(ringlog_test tests/ringlog_test.cc)
target_link_libraries(ringlog_test PRIVATE ringlog)

enable_testing()
add_test(NAME ringlog_test COMMAND ringlog_test)
