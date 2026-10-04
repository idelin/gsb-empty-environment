#!/bin/sh
# 主从写入模拟器场景脚本
# 用法: ./run.sh [partition|recovery|dup|all]
set -e
cd "$(dirname "$0")"
go run main.go "${1:-all}"
