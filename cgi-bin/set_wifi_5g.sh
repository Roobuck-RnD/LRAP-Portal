#!/bin/sh
echo "Content-Type: application/json"
echo ""

# 解析 QUERY_STRING 参数
query="$QUERY_STRING"
ip=$(echo "$query" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$query" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')
ssid=$(echo "$query" | sed -n 's/.*ssid=\([^&]*\).*/\1/p' | sed 's/%20/ /g')
password=$(echo "$query" | sed -n 's/.*password=\([^&]*\).*/\1/p')

bat_path="/etc/wireless/mediatek/mt7981.dbdc.b1.dat"

# 修改主模块 Wi-Fi 配置
modify_wifi_config() {
  local path="$1"
  [ -n "$ssid" ] && sed -i "s/^SSID1=.*/SSID1=$ssid/" "$path"
  [ -n "$password" ] && sed -i "s/^WPAPSK1=.*/WPAPSK1=$password/" "$path"
  (sleep 1; wifi reload >/dev/null 2>&1) &
  echo '{"success": true}'
}

# 远程修改子模块 Wi-Fi
remote_modify_wifi() {
  sshpass -p "admin" ssh -y -o ConnectTimeout=3 -o StrictHostKeyChecking=no root@"$ip" /bin/sh <<EOF
ssid='$ssid'
password='$password'
bat_path="/etc/wireless/mediatek/mt7981.dbdc.b1.dat"

[ ! -f "\$bat_path" ] && echo '{"success": false, "error": "Config not found"}' && exit 1

[ -n "\$ssid" ] && sed -i "s/^SSID1=.*/SSID1=\$ssid/" "\$bat_path"
[ -n "\$password" ] && sed -i "s/^WPAPSK1=.*/WPAPSK1=\$password/" "\$bat_path"

(sleep 1; wifi reload >/dev/null 2>&1) &

echo '{"success": true}'
EOF
}

# 判断主模块 or 子模块
if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  remote_modify_wifi
else
  if [ ! -f "$bat_path" ]; then
    echo '{"success": false, "error": "Configuration file not found"}'
    exit 1
  fi
  modify_wifi_config "$bat_path"
fi
