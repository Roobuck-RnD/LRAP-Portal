#!/bin/sh

# the MIME type must be declared
echo "Content-Type: application/json"
echo ""

ip=$(echo "$QUERY_STRING" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$QUERY_STRING" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

# read the POST data
read_json() {
  # read all of stdin into a variable
  read -r body
  echo "$body"
}

json=$(read_json)

user="root"
pass="admin"

# extract the fields from the JSON (busybox must have grep/sed/cut)
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

# sub-module: run remotely
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
  # main module: run locally
  uci add dhcp host
  uci set dhcp.@host[-1].name="$hostname"
  uci set dhcp.@host[-1].ip="$ipaddr"
  uci set dhcp.@host[-1].mac="$mac"
  uci commit dhcp
  /etc/init.d/dnsmasq restart
fi

# return the JSON response
echo '{"status":"success"}'
