#!/bin/sh
echo "Content-Type: application/json"
echo ""

ip=$(echo "$QUERY_STRING" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$QUERY_STRING" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

read_json() {
  # 将 stdin 全部读取到变量中
  read -r body
  echo "$body"
}

json=$(read_json)

user="root"
pass="admin"

get_value() {
  echo "$json" | grep -o "\"$1\":\"[^\"]*\"" | cut -d':' -f2- | sed 's/"//g'
}

mac=$(get_value mac)

remove_static_entry_local() {
  for idx in $(uci show dhcp | grep '=host' | cut -d'[' -f2 | cut -d']' -f1); do
    current_mac=$(uci get dhcp.@host[$idx].mac 2>/dev/null)
    echo "Comparing MAC: $current_mac vs $mac"
    if [ "$(echo "$current_mac" | tr 'a-z' 'A-Z')" = "$(echo "$mac" | tr 'a-z' 'A-Z')" ]; then
      echo "Deleting static lease for MAC $current_mac"
      uci delete dhcp.@host[$idx]
    fi
  done
  uci commit dhcp
  /etc/init.d/dnsmasq restart
}

echo "QUERY_STRING: $QUERY_STRING" >> /tmp/unset_static_debug.log
echo "ip: $ip" >> /tmp/unset_static_debug.log
echo "moduleType: $moduleType" >> /tmp/unset_static_debug.log
echo "mac: $mac" >> /tmp/unset_static_debug.log

if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$user@$ip" <<EOF
  mac="$mac"
  for idx in \$(uci show dhcp | grep '=host' | cut -d'[' -f2 | cut -d']' -f1); do
      current_mac=\$(uci get dhcp.@host[\$idx].mac 2>/dev/null)
      echo "Comparing MAC: \$current_mac vs \$mac"
      if [ "\$(echo "\$current_mac" | tr 'a-z' 'A-Z')" = "\$(echo "\$mac" | tr 'a-z' 'A-Z')" ]; then
        echo "Deleting static lease for MAC \$current_mac"
        uci delete dhcp.@host[\$idx]
      fi
  done
  uci commit dhcp
  /etc/init.d/dnsmasq restart
EOF
else
  remove_static_entry_local
fi

echo '{"status": "done"}'
