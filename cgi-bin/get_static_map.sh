#!/bin/sh
echo "Content-Type: application/json"
echo ""

ip=$(echo "$QUERY_STRING" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$QUERY_STRING" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

user="root"
pass="admin"

get_static_map() {
  echo "{"
  first=true
  uci show dhcp | grep '=host' | cut -d'[' -f2 | cut -d']' -f1 | while read index; do
  mac=$(uci get dhcp.@host[$index].mac 2>/dev/null)

  if [ -n "$mac" ]; then
    # comma handling (JSON)
    if [ "$first" -eq 0 ]; then
      echo -n ","
    fi
    first=0

    # output a "mac": true pair
    echo -n "\"$mac\": true"
  fi
done
  echo "}"
}

if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -y "$user@$ip" 'sh -s' <<'EOF'
    echo "{"
    first=true
    uci show dhcp | grep '=host' | cut -d'[' -f2 | cut -d']' -f1 | while read index; do
      mac=$(uci get dhcp.@host[$index].mac 2>/dev/null)

      if [ -n "$mac" ]; then
        # comma handling (JSON)
        if [ "$first" -eq 0 ]; then
          echo -n ","
        fi
        first=0

        # output a "mac": true pair
        echo -n "\"$mac\": true"
      fi
    done
    echo "}"
EOF
else
  get_static_map
fi
