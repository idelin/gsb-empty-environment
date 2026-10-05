cmake_minimum_required(VERSION 3.16)
project(filelock CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE RelWithDebInfo)
endif()

add_library(filelock
  src/lock_core.cpp
  src/lock_manager.cpp
)
target_include_directories(filelock PUBLIC include)
target_compile_options(filelock PRIVATE -Wall -Wextra)

find_package(Threads REQUIRED)
target_link_libraries(filelock PUBLIC Threads::Threads)

add_executable(filelock_tests
  tests/test_main.cpp
  tests/unit_tests.cpp
  tests/interleaving_tests.cpp
  tests/model_tests.cpp
)
target_include_directories(filelock_tests PRIVATE tests)
target_compile_options(filelock_tests PRIVATE -Wall -Wextra)
target_link_libraries(filelock_tests PRIVATE filelock)

enable_testing()
add_test(NAME filelock_tests COMMAND filelock_tests)
