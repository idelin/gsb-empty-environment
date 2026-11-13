cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

if(APPLE AND CMAKE_AR STREQUAL "/usr/bin/ar")
  execute_process(
    COMMAND "${CMAKE_CXX_COMPILER}" -print-prog-name=ar
    OUTPUT_VARIABLE _netsec_ar
    OUTPUT_STRIP_TRAILING_WHITESPACE)
  execute_process(
    COMMAND "${CMAKE_CXX_COMPILER}" -print-prog-name=ranlib
    OUTPUT_VARIABLE _netsec_ranlib
    OUTPUT_STRIP_TRAILING_WHITESPACE)
  if(EXISTS "${_netsec_ar}" AND EXISTS "${_netsec_ranlib}")
    set(CMAKE_AR "${_netsec_ar}" CACHE FILEPATH "Archiver" FORCE)
    set(CMAKE_RANLIB "${_netsec_ranlib}" CACHE FILEPATH "Ranlib" FORCE)
  endif()
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
set_target_properties(netsec PROPERTIES POSITION_INDEPENDENT_CODE ON)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
