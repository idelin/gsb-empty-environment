#----------------------------------------------------------------
# Generated CMake target import file for configuration "Release".
#----------------------------------------------------------------

# Commands may need to know the format version.
set(CMAKE_IMPORT_FILE_VERSION 1)

# Import target "netsec::netsec" for configuration "Release"
set_property(TARGET netsec::netsec APPEND PROPERTY IMPORTED_CONFIGURATIONS RELEASE)
set_target_properties(netsec::netsec PROPERTIES
  IMPORTED_LINK_INTERFACE_LANGUAGES_RELEASE "CXX"
  IMPORTED_LOCATION_RELEASE "${_IMPORT_PREFIX}/lib/libnetsec.a"
  )

list(APPEND _cmake_import_check_targets netsec::netsec )
list(APPEND _cmake_import_check_files_for_netsec::netsec "${_IMPORT_PREFIX}/lib/libnetsec.a" )

# Commands beyond this point should not need to know the version.
set(CMAKE_IMPORT_FILE_VERSION)
