#!/bin/sh
echo "Content-Type: application/json"
echo ""

# === 解析参数 ===
query="$QUERY_STRING"
ip=$(echo "$query" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$query" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

# === 本地获取 2G 和 5G SSID ===
get_local_ssid() {
  ssid_2g=$(iwinfo ra0 info 2>/dev/null | awk -F'"' '/ESSID/ {print $2}')
  ssid_5g=$(iwinfo rax0 info 2>/dev/null | awk -F'"' '/ESSID/ {print $2}')

  [ -z "$ssid_2g" ] && ssid_2g="(unknown)"
  [ -z "$ssid_5g" ] && ssid_5g="(unknown)"

  echo "{\"wifi_2g\": \"$ssid_2g\", \"wifi_5g\": \"$ssid_5g\"}"
}

# === 子模块远程获取 SSID ===
get_remote_ssid() {
  sshpass -p 'admin' ssh -y -o ConnectTimeout=3 -o StrictHostKeyChecking=no root@"$ip" 'sh -s' <<'EOF'
ssid_2g=$(iwinfo ra0 info 2>/dev/null | awk -F'"' '/ESSID/ {print $2}')
ssid_5g=$(iwinfo rax0 info 2>/dev/null | awk -F'"' '/ESSID/ {print $2}')

[ -z "$ssid_2g" ] && ssid_2g="(unknown)"
[ -z "$ssid_5g" ] && ssid_5g="(unknown)"

echo "{\"wifi_2g\": \"$ssid_2g\", \"wifi_5g\": \"$ssid_5g\"}"
EOF
}

# === 路由判断 ===
if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  get_remote_ssid
else
  get_local_ssid
fi
