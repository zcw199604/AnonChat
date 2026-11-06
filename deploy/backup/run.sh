#!/bin/sh
# 备份 AnonChat 数据库为自定义格式，并只保留最近 7 份。
set -eu
ts=$(date +%Y%m%d-%H%M%S)
out="/backups/anonchat-$ts.dump"
pg_dump -Fc -f "$out"
echo "备份已写入: $out"
# 保留最近 7 份
ls -1t /backups/anonchat-*.dump 2>/dev/null | tail -n +8 | while read -r f; do
  rm -f "$f"
  echo "删除旧备份: $f"
done
