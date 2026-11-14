cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some macOS CI images expose /usr/bin/ar as an xcode-select shim without an
# active developer directory. Prefer the CommandLineTools archiver when the
# system shim is not usable, otherwise a plain `cmake --build` cannot archive
# the static library.
if(APPLE)
  execute_process(COMMAND /usr/bin/ar --version
                  RESULT_VARIABLE NETSEC_SYSTEM_AR_STATUS
                  OUTPUT_QUIET ERROR_QUIET)
  if(NOT NETSEC_SYSTEM_AR_STATUS EQUAL 0)
    set(NETSEC_CLT_BIN "/Library/Developer/CommandLineTools/usr/bin")
    if(EXISTS "${NETSEC_CLT_BIN}/ar" AND EXISTS "${NETSEC_CLT_BIN}/ranlib")
      set(CMAKE_CXX_ARCHIVE_CREATE
          "${NETSEC_CLT_BIN}/ar qc <TARGET> <LINK_FLAGS> <OBJECTS>")
      set(CMAKE_CXX_ARCHIVE_APPEND
          "${NETSEC_CLT_BIN}/ar q <TARGET> <LINK_FLAGS> <OBJECTS>")
      set(CMAKE_CXX_ARCHIVE_FINISH "${NETSEC_CLT_BIN}/ranlib <TARGET>")
    endif()
  endif()
endif()

include(GNUInstallDirs)

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
  $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>)
target_compile_features(netsec PUBLIC cxx_std_20)

if(MSVC)
  target_compile_options(netsec PRIVATE /W4)
else()
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic -Wconversion)
endif()

enable_testing()

add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
if(MSVC)
  target_compile_options(netsec_tests PRIVATE /W4)
else()
  target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic -Wconversion)
endif()
add_test(NAME netsec_tests COMMAND netsec_tests)

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets NAMESPACE netsec::
        FILE netsecConfig.cmake
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
