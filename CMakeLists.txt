cmake_minimum_required(VERSION 3.16)
project(evlib CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(evlib INTERFACE)
target_include_directories(evlib INTERFACE ${CMAKE_CURRENT_SOURCE_DIR}/include)

find_package(Threads REQUIRED)

add_executable(test_event tests/test_event.cpp)
target_link_libraries(test_event PRIVATE evlib Threads::Threads)
if(NOT MSVC)
    target_compile_options(test_event PRIVATE -Wall -Wextra -Wpedantic)
endif()

enable_testing()
add_test(NAME event COMMAND test_event)
