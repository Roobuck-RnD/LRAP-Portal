#!/bin/sh
echo "Content-Type: application/json"
echo ""

# 获取 CGI 参数
ip=$(echo "$QUERY_STRING" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$QUERY_STRING" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

user="root"
pass="admin"

format_time() {
  seconds=$1
  [ -z "$seconds" ] && seconds=0
  hours=$((seconds / 3600))
  mins=$(( (seconds % 3600) / 60 ))
  echo "${hours} h ${mins} m"
}

get_local_info() {
  json=$(ifstatus wan 2>/dev/null)
  [ -z "$json" ] && echo '{}' && exit 0

  proto=$(echo "$json" | jsonfilter -e '@.proto')
  address=$(echo "$json" | jsonfilter -e '@["ipv4-address"][0].address')
  device=$(echo "$json" | jsonfilter -e '@.device')
  gateway=$(echo "$json" | jsonfilter -e '@["route"][0].nexthop')
  dns=$(echo "$json" | jsonfilter -e '@["dns-server"][0]')
  expires=$(echo "$json" | jsonfilter -e '@["data"].leasetime')
  connected=$(echo "$json" | jsonfilter -e '@.uptime')
  mac=$(ip link show "$device" 2>/dev/null | awk '/ether/ {print $2}')

  echo "{
    \"protocol\": \"${proto:-unknown}\",
    \"address\": \"${address:-unknown}\",
    \"device\": \"${device:-unknown}\",
    \"gateway\": \"${gateway:-unknown}\",
    \"dns\": \"${dns:-unknown}\",
    \"expires\": \"$(format_time ${expires:-0})\",
    \"connected\": \"$(format_time ${connected:-0})\",
    \"mac\": \"${mac:-unknown}\"
  }"
}

get_remote_info() {
sshpass -p "$pass" ssh -y -o ConnectTimeout=3 "$user@$ip" 'sh -s' <<'EOF'
  format_time() {
    seconds=$1
    [ -z "$seconds" ] && seconds=0
    hours=$((seconds / 3600))
    mins=$(( (seconds % 3600) / 60 ))
    echo "${hours} h ${mins} min"
  }

  json=$(ifstatus wan 2>/dev/null)
  [ -z "$json" ] && echo '{}' && exit 0

  proto=$(echo "$json" | jsonfilter -e '@.proto')
  address=$(echo "$json" | jsonfilter -e '@["ipv4-address"][0].address')
  device=$(echo "$json" | jsonfilter -e '@.device')
  gateway=$(echo "$json" | jsonfilter -e '@["route"][0].nexthop')
  dns=$(echo "$json" | jsonfilter -e '@["dns-server"][0]')
  expires=$(echo "$json" | jsonfilter -e '@["data"].leasetime')
  connected=$(echo "$json" | jsonfilter -e '@.uptime')
  mac=$(ip link show "$device" 2>/dev/null | awk "/ether/ {print \$2}")

  echo "{
    \"protocol\": \"${proto:-unknown}\",
    \"address\": \"${address:-unknown}\",
    \"device\": \"${device:-unknown}\",
    \"gateway\": \"${gateway:-unknown}\",
    \"dns\": \"${dns:-unknown}\",
    \"expires\": \"$(format_time ${expires:-0})\",
    \"connected\": \"$(format_time ${connected:-0})\",
    \"mac\": \"${mac:-unknown}\"
  }"
EOF
}

if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  get_remote_info
else
  get_local_info
fi
