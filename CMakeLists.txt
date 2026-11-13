cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(APPLE)
  execute_process(
    COMMAND /usr/bin/ar --version
    RESULT_VARIABLE NETSEC_SYSTEM_AR_RESULT
    OUTPUT_QUIET
    ERROR_QUIET)
  if(NOT NETSEC_SYSTEM_AR_RESULT EQUAL 0)
    set(NETSEC_COMMAND_LINE_TOOLS "/Library/Developer/CommandLineTools/usr/bin")
    if(EXISTS "${NETSEC_COMMAND_LINE_TOOLS}/ar")
      set(CMAKE_AR "${NETSEC_COMMAND_LINE_TOOLS}/ar"
        CACHE FILEPATH "" FORCE)
    endif()
    if(EXISTS "${NETSEC_COMMAND_LINE_TOOLS}/ranlib")
      set(CMAKE_RANLIB "${NETSEC_COMMAND_LINE_TOOLS}/ranlib"
        CACHE FILEPATH "" FORCE)
    endif()
  endif()
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(netsec_tests tests/test_netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
