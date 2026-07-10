#!/bin/sh
echo "Content-Type: application/json"
echo ""

query="$QUERY_STRING"
ip=$(echo "$query" | sed -n 's/.*ip=\([^&]*\).*/\1/p')
module_type=$(echo "$query" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g; s/+/ /g')
new_hostname=$(echo "$query" | sed -n 's/.*hostname=\([^&]*\).*/\1/p' | sed 's/%20/ /g; s/+/ /g')

user="root"
pass="admin"

get_hostname_cmd="cat /proc/sys/kernel/hostname"
set_hostname_cmd="uci set system.@system[0].hostname='$new_hostname'; uci commit system; /etc/init.d/system reload"

# renew_dhcp_cmd="udhcpc -i br-lan -n -q -t 1 -x hostname:$new_hostname"

if [ "$module_type" = "Main Module" ]; then
  if [ -n "$new_hostname" ]; then
    eval "$set_hostname_cmd"
  fi
  echo "{\"hostname\": \"$(eval "$get_hostname_cmd")\"}"
else
  if [ -n "$new_hostname" ]; then
    sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$user@$ip" "$set_hostname_cmd"
    /etc/init.d/dnsmasq restart
  fi
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$user@$ip" "$get_hostname_cmd" | awk '{ print "{\"hostname\": \"" $0 "\"}" }'
fi
