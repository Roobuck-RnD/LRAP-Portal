#!/bin/sh
echo "Content-Type: application/json"
echo ""

query="$QUERY_STRING"
ip=$(echo "$query" | sed -n 's/.*ip=\([^&]*\).*/\1/p')
module_type=$(echo "$query" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g; s/+/ /g')

user="root"
pass="admin"

# 本地 ARP
get_arp_json_from_file() {
  awk 'NR > 1 && $3 != "0x0" {
    if (count++ > 0) printf(",\n")
    printf("  {\"ip\": \"%s\", \"mac\": \"%s\", \"interface\": \"%s\"}", $1, $4, $6)
  }' /proc/net/arp
}

get_local_arp() {
  echo "["
  get_arp_json_from_file
  echo "]"
}

# 远程 ARP
get_remote_arp() {
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$user@$ip" 'cat /proc/net/arp' | awk '
  BEGIN { print "[" }
  NR > 1 && $3 != "0x0" {
    if (count++ > 0) printf(",\n")
    printf("  {\"ip\": \"%s\", \"mac\": \"%s\", \"interface\": \"%s\"}", $1, $4, $6)
  }
  END { print "\n]" }
  '
}

# 执行
if [ "$module_type" = "Main Module" ]; then
  get_local_arp
else
  get_remote_arp
fi
