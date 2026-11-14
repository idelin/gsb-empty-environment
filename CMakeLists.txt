cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

if(APPLE)
  execute_process(COMMAND /usr/bin/ar --version
                  RESULT_VARIABLE NETSEC_SYSTEM_AR_RESULT
                  OUTPUT_QUIET ERROR_QUIET)
  if(NOT NETSEC_SYSTEM_AR_RESULT EQUAL 0)
    set(NETSEC_CLT_BIN "/Library/Developer/CommandLineTools/usr/bin")
    if(EXISTS "${NETSEC_CLT_BIN}/ar" AND EXISTS "${NETSEC_CLT_BIN}/ranlib")
      set(CMAKE_AR "${NETSEC_CLT_BIN}/ar" CACHE FILEPATH "Archiver" FORCE)
      set(CMAKE_RANLIB "${NETSEC_CLT_BIN}/ranlib" CACHE FILEPATH "Ranlib" FORCE)
    endif()
  endif()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(GNUInstallDirs)
install(TARGETS netsec
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")

enable_testing()

add_executable(netsec_tests tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
