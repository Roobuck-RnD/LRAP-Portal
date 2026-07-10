#!/bin/sh

echo "Content-Type: application/json"
echo ""

ip=$(echo "$QUERY_STRING" | sed -n 's/.*ip=\([^&]*\).*/\1/p' | sed 's/%2E/./g')
moduleType=$(echo "$QUERY_STRING" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g')

user="root"
pass="admin"

parse_leases() {
  leases_file="$1"
  if [ ! -f "$leases_file" ]; then
    echo "[]"
    return
  fi
  echo "["
  first=true
  while read -r line; do
    set -- $line
    mac=$(echo "$2" | tr 'a-z' 'A-Z')
    ip=$3
    hostname=$4
    expires_sec=$(( $1 - $(date +%s) ))
    [ $expires_sec -lt 0 ] && expires_sec=0
    h=$((expires_sec / 3600))
    m=$(( (expires_sec % 3600) / 60 ))
    s=$((expires_sec % 60))
    expires="${h}h ${m}m ${s}s"
    if [ "$hostname" = "*" ] || [ "$hostname" = "* *" ] || [ "$hostname" = "assets" ]; then
      hostname="(unknown)"
    fi

    if [ "$first" = true ]; then
      first=false
    else
      echo ","
    fi

    echo "  {\"hostname\": \"$hostname\", \"ip\": \"$ip\", \"mac\": \"$mac\", \"expires\": \"$expires\"}"
  done < "$leases_file"
  echo "]"
}

# 子模块
if [ "$moduleType" = "Sub Module" ] && [ -n "$ip" ]; then
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -y "$user@$ip" 'sh -s' <<'EOF'
    if [ ! -f /tmp/dhcp.leases ]; then echo "[]"; exit; fi
    echo "["
    first=true
    while read -r line; do
      set -- $line
      mac=$(echo "$2" | tr 'a-z' 'A-Z')
      ip=$3
      hostname=$4
      expires_sec=$(( $1 - $(date +%s) ))
      [ $expires_sec -lt 0 ] && expires_sec=0
      h=$((expires_sec / 3600))
      m=$(( (expires_sec % 3600) / 60 ))
      s=$((expires_sec % 60))
      expires="${h}h ${m}m ${s}s"
      if [ "$hostname" = "*" ] || [ "$hostname" = "* *" ] || [ "$hostname" = "assets" ]; then
        hostname="(unknown)"
      fi

      if [ "$first" = true ]; then
        first=false
      else
        echo ","
      fi

      echo "  {\"hostname\": \"$hostname\", \"ip\": \"$ip\", \"mac\": \"$mac\", \"expires\": \"$expires\"}"
    done < /tmp/dhcp.leases
    echo "]"
EOF
  exit 0
fi

# 主模块本地处理
parse_leases /tmp/dhcp.leases
