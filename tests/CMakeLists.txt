add_executable(netsec_tests test_biometric_template.cpp)
target_link_libraries(netsec_tests PRIVATE netsec::netsec)
target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)

add_test(NAME biometric_template COMMAND netsec_tests)
