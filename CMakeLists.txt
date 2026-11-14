cmake_minimum_required(VERSION 3.16)
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
  set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
      CACHE FILEPATH "" FORCE)
  set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
      CACHE FILEPATH "" FORCE)
endif()
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
set_target_properties(netsec PROPERTIES CXX_STANDARD 20 CXX_STANDARD_REQUIRED ON
                                        CXX_EXTENSIONS OFF)
if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()
enable_testing()

add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
set_target_properties(netsec_tests PROPERTIES CXX_STANDARD 20
                                             CXX_STANDARD_REQUIRED ON
                                             CXX_EXTENSIONS OFF)
if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
endif()
add_test(NAME netsec_tests COMMAND netsec_tests)
