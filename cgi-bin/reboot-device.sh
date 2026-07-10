#!/bin/sh
echo "Content-Type: application/json"
echo ""

query="$QUERY_STRING"
ip=$(echo "$query" | sed -n 's/.*ip=\([^&]*\).*/\1/p')
module_type=$(echo "$query" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g; s/+/ /g')

user="root"
pass="admin"

if [ "$module_type" = "Main Module" ]; then
  (sleep 1 && reboot) &
  echo '{"success": true, "message": "Main module is rebooting..."}'
else
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$user@$ip" "(sleep 1 && reboot) &" >/dev/null 2>&1

  if [ $? -eq 0 ]; then
    echo "{\"success\": true, \"message\": \"Sub module at $ip is rebooting...\"}"
  else
    echo "{\"success\": false, \"error\": \"Failed to reboot sub module at $ip\"}"
  fi
fi
