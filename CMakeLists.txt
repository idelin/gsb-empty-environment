cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some headless macOS installs expose only compiler shims in /usr/bin. Ask the
# selected compiler for the real archiver instead of inheriting a broken shim.
if(APPLE)
  execute_process(
    COMMAND "${CMAKE_CXX_COMPILER}" -print-prog-name=ar
    OUTPUT_VARIABLE NETSEC_ARCHIVER
    OUTPUT_STRIP_TRAILING_WHITESPACE
    RESULT_VARIABLE NETSEC_ARCHIVER_RESULT)
  execute_process(
    COMMAND "${CMAKE_CXX_COMPILER}" -print-prog-name=ranlib
    OUTPUT_VARIABLE NETSEC_RANLIB
    OUTPUT_STRIP_TRAILING_WHITESPACE
    RESULT_VARIABLE NETSEC_RANLIB_RESULT)
  if(NETSEC_ARCHIVER_RESULT EQUAL 0 AND EXISTS "${NETSEC_ARCHIVER}")
    set(CMAKE_AR "${NETSEC_ARCHIVER}" CACHE FILEPATH "" FORCE)
  endif()
  if(NETSEC_RANLIB_RESULT EQUAL 0 AND EXISTS "${NETSEC_RANLIB}")
    set(CMAKE_RANLIB "${NETSEC_RANLIB}" CACHE FILEPATH "" FORCE)
  endif()
endif()

find_package(Threads REQUIRED)
add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
target_link_libraries(netsec PUBLIC Threads::Threads)
set_target_properties(netsec PROPERTIES POSITION_INDEPENDENT_CODE ON)
enable_testing()

if(CMAKE_SOURCE_DIR STREQUAL PROJECT_SOURCE_DIR)
  set(NETSEC_TESTS_DEFAULT ON)
else()
  set(NETSEC_TESTS_DEFAULT OFF)
endif()
option(NETSEC_BUILD_TESTS "Build credential component tests"
       ${NETSEC_TESTS_DEFAULT})
if(NETSEC_BUILD_TESTS)
  add_executable(netsec_tests tests/netsec_tests.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec Threads::Threads)
  add_test(NAME netsec_tests COMMAND netsec_tests)
endif()
