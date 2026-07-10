#!/bin/sh

# 必须声明 MIME 类型
echo "Content-Type: application/json"
echo ""

ip=$(echo "$QUERY_STRING" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$QUERY_STRING" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

# 读取 POST 数据
read_json() {
  # 将 stdin 全部读取到变量中
  read -r body
  echo "$body"
}

json=$(read_json)

user="root"
pass="admin"

# 从 JSON 中提取字段（确保 busybox 有 grep/sed/cut 支持）
get_value() {
  echo "$json" | grep -o "\"$1\":\"[^\"]*\"" | cut -d':' -f2- | sed 's/"//g'
}

hostname=$(get_value hostname)
mac=$(get_value mac)
ipaddr=$(get_value ipaddr)

# echo "QUERY_STRING: $QUERY_STRING" >> /tmp/set_static_debug.log
# echo "ip: $ip" >> /tmp/set_static_debug.log
# echo "moduleType: $moduleType" >> /tmp/set_static_debug.log
# echo "hostname: $hostname" >> /tmp/set_static_debug.log
# echo "mac: $mac" >> /tmp/set_static_debug.log
# echo "ipaddr: $ipaddr" >> /tmp/set_static_debug.log

# 子模块远程执行
# if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
#   sshpass -p "$pass" ssh -o ConnectTimeout=3 -y "$user@$ip" <<EOF
#     uci add dhcp host
#     uci set dhcp.@host[-1].name='$hostname'
#     uci set dhcp.@host[-1].ip='$ipaddr'
#     uci set dhcp.@host[-1].mac='$mac'
#     uci commit dhcp
#     /etc/init.d/dnsmasq restart
# EOF
if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$user@$ip" <<EOF
    uci add dhcp host
    uci set dhcp.@host[-1].name='$hostname'
    uci set dhcp.@host[-1].ip='$ipaddr'
    uci set dhcp.@host[-1].mac='$mac'
    uci commit dhcp
    /etc/init.d/dnsmasq restart
    
EOF
else
  # 主模块本地执行
  uci add dhcp host
  uci set dhcp.@host[-1].name="$hostname"
  uci set dhcp.@host[-1].ip="$ipaddr"
  uci set dhcp.@host[-1].mac="$mac"
  uci commit dhcp
  /etc/init.d/dnsmasq restart
fi

# 返回 JSON 响应
echo '{"status":"success"}'
