#!/bin/sh
echo "Content-Type: application/json"
echo ""

echo "["

first=true

### ✅ 0. ping every DHCP IP so the FDB learns them
awk '{print $3}' /tmp/dhcp.leases | while read ip; do
  ping -c 1 -W 1 "$ip" >/dev/null 2>&1 &
done

### ✅ 1. add the main module's LAN information
lan_if="br-lan"
# lan_mac=$(bridge fdb show br br-lan | grep "dev br-lan vlan 1 master br-lan permanent" | awk '{print toupper($1)}')
lan_mac=$(ip link show "$lan_if" | awk '/link\/ether/ {print toupper($2)}')
lan_ip=$(ip -4 addr show "$lan_if" | awk '/inet/ {print $2}' | cut -d/ -f1)
lan_hostname=$(uci get system.@system[0].hostname 2>/dev/null)
[ -z "$lan_hostname" ] && lan_hostname=$(hostname)
[ -z "$lan_hostname" ] && lan_hostname="(unknown)"

# output the main module entry
echo "  {"
echo "    \"port\": \"$lan_if\","
echo "    \"mac\": \"$lan_mac\","
echo "    \"ip\": \"${lan_ip:-\"(unknown)\"}\","
echo "    \"hostname\": \"$lan_hostname\""
echo "  }"

first=false

### ✅ 2. walk the bridge devices on lan1~lan4 to find the sub-modules
bridge fdb show br br-lan | \
grep -E "dev lan[1-4] " | \
grep -v vlan | \
grep -v self | \
grep -v permanent | \
while read line; do
  mac=$(echo "$line" | awk '{print $1}' | tr 'a-z' 'A-Z')
  port=$(echo "$line" | awk '{print $3}')

  # match the MAC in dhcp.leases to get the IP and hostname
  lease_info=$(awk -v mac="$mac" 'toupper($2)==mac {print $3, $4}' /tmp/dhcp.leases)
  ip=$(echo "$lease_info" | awk '{print $1}')
  hostname=$(echo "$lease_info" | awk '{print $2}')

  # a hostname of * or empty becomes (unknown)
  [ "$hostname" = "*" ] || [ -z "$hostname" ] && hostname="(unknown)"
  [ -z "$ip" ] && ip="(unknown)"

  # output JSON
  echo ","
  echo "  {"
  echo "    \"port\": \"$port\","
  echo "    \"mac\": \"$mac\","
  echo "    \"ip\": \"$ip\","
  echo "    \"hostname\": \"$hostname\""
  echo "  }"
done

echo "]"
