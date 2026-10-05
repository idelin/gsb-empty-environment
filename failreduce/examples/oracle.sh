#!/bin/sh
# Demo oracle: judges a configuration passed as argv items.
#   exit 2 (compile failure) if "logging" is enabled without "auth"
#   exit 1 (target test failure) if "db" and "cache" are both enabled
#   exit 0 otherwise
has_logging=0; has_auth=0; has_db=0; has_cache=0
for item in "$@"; do
  case "$item" in
    logging) has_logging=1 ;;
    auth)    has_auth=1 ;;
    db)      has_db=1 ;;
    cache)   has_cache=1 ;;
  esac
done
if [ "$has_logging" = 1 ] && [ "$has_auth" = 0 ]; then
  echo "compile error: logging requires auth" >&2
  exit 2
fi
if [ "$has_db" = 1 ] && [ "$has_cache" = 1 ]; then
  echo "TEST FAILURE: cache/db deadlock reproduced" >&2
  exit 1
fi
exit 0
