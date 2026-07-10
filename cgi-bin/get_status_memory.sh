#!/bin/sh

echo "Content-Type: application/json"
echo ""

ip=$(echo "$QUERY_STRING" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$QUERY_STRING" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

get_local_memory() {
  total=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
  available=$(awk '/MemAvailable/ {print $2}' /proc/meminfo)
  used=$((total - available))
  buffered=$(awk '/^Buffers:/ {print $2}' /proc/meminfo)
  cached=$(awk '/^Cached:/ {print $2}' /proc/meminfo)

  echo "{
    \"total\": $total,
    \"available\": $available,
    \"used\": $used,
    \"buffered\": $buffered,
    \"cached\": $cached
  }"
}

get_remote_memory() {
  sshpass -p 'admin' ssh -y -o ConnectTimeout=3 root@"$ip" 'sh -s' <<'EOF'
    total=$(awk "/MemTotal/ {print \$2}" /proc/meminfo)
    available=$(awk "/MemAvailable/ {print \$2}" /proc/meminfo)
    used=$((total - available))
    buffered=$(awk "/^Buffers:/ {print \$2}" /proc/meminfo)
    cached=$(awk "/^Cached:/ {print \$2}" /proc/meminfo)

    echo "{
      \"total\": $total,
      \"available\": $available,
      \"used\": $used,
      \"buffered\": $buffered,
      \"cached\": $cached
    }"
EOF
}

if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  get_remote_memory
else
  get_local_memory
fi
