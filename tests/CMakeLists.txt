add_executable(netsec_tests test_netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)

if(MSVC)
  target_compile_options(netsec_tests PRIVATE /W4)
else()
  target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic -O2)
endif()

add_test(NAME netsec_tests COMMAND netsec_tests)

if(NOT MSVC)
  add_test(
    NAME netsec_barrier_asm
    COMMAND ${CMAKE_COMMAND}
      "-DCXX=${CMAKE_CXX_COMPILER}"
      "-DSRC=${CMAKE_CURRENT_SOURCE_DIR}/barrier_probe.cpp"
      "-DINC=${PROJECT_SOURCE_DIR}"
      "-DOUT=${CMAKE_CURRENT_BINARY_DIR}/barrier_probe.s"
      "-DPROC=${CMAKE_SYSTEM_PROCESSOR}"
      -P "${CMAKE_CURRENT_SOURCE_DIR}/check_barrier_asm.cmake")
endif()
