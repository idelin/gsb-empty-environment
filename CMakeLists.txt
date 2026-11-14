cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some macOS build images have no active xcode-select, so /usr/bin/ar and
# /usr/bin/ranlib are failing shims. Use the Command Line Tools archive
# tools when they are installed at their standard location.
if(APPLE)
  execute_process(
    COMMAND "${CMAKE_AR}" --version
    RESULT_VARIABLE NETSEC_AR_RESULT
    OUTPUT_QUIET
    ERROR_QUIET)
  if(NOT NETSEC_AR_RESULT EQUAL 0)
    set(NETSEC_CLT_BIN "/Library/Developer/CommandLineTools/usr/bin")
    if(EXISTS "${NETSEC_CLT_BIN}/ar" AND EXISTS "${NETSEC_CLT_BIN}/ranlib")
      set(CMAKE_AR "${NETSEC_CLT_BIN}/ar" CACHE FILEPATH "Archiver" FORCE)
      set(CMAKE_RANLIB "${NETSEC_CLT_BIN}/ranlib" CACHE FILEPATH "Ranlib" FORCE)
    endif()
  endif()
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
enable_testing()
add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
