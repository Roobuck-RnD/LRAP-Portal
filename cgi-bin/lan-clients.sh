#!/bin/sh
echo "Content-Type: application/json"
echo ""

echo "["

first=true

### ✅ 0. 主动 ping 所有 DHCP IP，触发 FDB 注册
awk '{print $3}' /tmp/dhcp.leases | while read ip; do
  ping -c 1 -W 1 "$ip" >/dev/null 2>&1 &
done

### ✅ 1. 添加主模块 LAN 信息
lan_if="br-lan"
# lan_mac=$(bridge fdb show br br-lan | grep "dev br-lan vlan 1 master br-lan permanent" | awk '{print toupper($1)}')
lan_mac=$(ip link show "$lan_if" | awk '/link\/ether/ {print toupper($2)}')
lan_ip=$(ip -4 addr show "$lan_if" | awk '/inet/ {print $2}' | cut -d/ -f1)
lan_hostname=$(uci get system.@system[0].hostname 2>/dev/null)
[ -z "$lan_hostname" ] && lan_hostname=$(hostname)
[ -z "$lan_hostname" ] && lan_hostname="(unknown)"

# 输出主模块项
echo "  {"
echo "    \"port\": \"$lan_if\","
echo "    \"mac\": \"$lan_mac\","
echo "    \"ip\": \"${lan_ip:-\"(unknown)\"}\","
echo "    \"hostname\": \"$lan_hostname\""
echo "  }"

first=false

### ✅ 2. 遍历 lan1~lan4 的桥接设备，获取子模块信息
bridge fdb show br br-lan | \
grep -E "dev lan[1-4] " | \
grep -v vlan | \
grep -v self | \
grep -v permanent | \
while read line; do
  mac=$(echo "$line" | awk '{print $1}' | tr 'a-z' 'A-Z')
  port=$(echo "$line" | awk '{print $3}')

  # 在 dhcp.leases 中匹配 MAC，提取 IP 和 Hostname
  lease_info=$(awk -v mac="$mac" 'toupper($2)==mac {print $3, $4}' /tmp/dhcp.leases)
  ip=$(echo "$lease_info" | awk '{print $1}')
  hostname=$(echo "$lease_info" | awk '{print $2}')

  # 如果 hostname 是 * 或为空，替换为 (unknown)
  [ "$hostname" = "*" ] || [ -z "$hostname" ] && hostname="(unknown)"
  [ -z "$ip" ] && ip="(unknown)"

  # 输出 JSON
  echo ","
  echo "  {"
  echo "    \"port\": \"$port\","
  echo "    \"mac\": \"$mac\","
  echo "    \"ip\": \"$ip\","
  echo "    \"hostname\": \"$hostname\""
  echo "  }"
done

echo "]"
