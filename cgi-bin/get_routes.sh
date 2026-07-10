#!/bin/sh
echo "Content-Type: application/json"
echo ""

query="$QUERY_STRING"
ip=$(echo "$query" | sed -n 's/.*ip=\([^&]*\).*/\1/p')
module_type=$(echo "$query" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g; s/+/ /g')

user="root"
pass="admin"

parse_routes() {
  awk '
  {
    iface = "-"; target = "-"; gateway = "-"; metric = "0"; table = "main";

    if ($1 == "default") {
      target = "0.0.0.0/0";
      for (i = 1; i <= NF; i++) {
        if ($i == "via") gateway = $(i + 1);
        if ($i == "dev") iface = $(i + 1);
      }
    } else {
      target = $1;
      for (i = 1; i <= NF; i++) {
        if ($i == "dev") iface = $(i + 1);
        if ($i == "via") gateway = $(i + 1);
      }
    }

    # Set network label
    if (iface ~ /br-lan|lan/) {
      network = "lan"
    } else if (iface ~ /eth[0-9]/) {
      network = "wan"
    } else {
      network = iface
    }

    if (count++ > 0) printf(",\n")
    printf("  {\"network\": \"%s\", \"target\": \"%s\", \"gateway\": \"%s\", \"metric\": \"%s\", \"table\": \"%s\"}",
           network, target, gateway, metric, table)
  }
  '
}

get_local_routes() {
  echo "["
  ip route show | parse_routes
  echo "]"
}

get_remote_routes() {
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$user@$ip" 'ip route show' | parse_routes | awk '
  BEGIN { print "[" }
  { print }
  END { print "]" }
  '
}

if [ "$module_type" = "Main Module" ]; then
  get_local_routes
else
  get_remote_routes
fi
