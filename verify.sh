#!/bin/sh
# 三副本分区验证：删除后并发重加不被吞掉，离线副本未确认前不丢弃删除记录。
set -e
cd "$(dirname "$0")"

# 若默认构建缓存不可写（如沙箱环境），回退到临时目录。
cache_dir="${GOCACHE:-$(go env GOCACHE)}"
if ! touch "$cache_dir/.orset-w" 2>/dev/null; then
	export GOCACHE="${TMPDIR:-/tmp}/orset-gocache"
	mkdir -p "$GOCACHE"
else
	rm -f "$cache_dir/.orset-w"
fi

go run ./cmd/verify
