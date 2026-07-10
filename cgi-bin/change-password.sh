#!/bin/sh
echo "Content-Type: application/json"
echo ""

exec 2>/dev/null  # 屏蔽错误输出，避免干扰 JSON

query="$QUERY_STRING"
username=$(echo "$query" | sed -n 's/.*username=\([^&]*\).*/\1/p')
password=$(echo "$query" | sed -n 's/.*password=\([^&]*\).*/\1/p')

if [ -z "$username" ] || [ -z "$password" ]; then
  echo '{"success": false, "error": "Missing username or password"}'
  exit 0
fi

if ! id "$username" >/dev/null 2>&1; then
  echo '{"success": false, "error": "User does not exist"}'
  exit 0
fi

echo -e "$password\n$password" | passwd "$username" >/dev/null 2>&1

if [ $? -eq 0 ]; then
  (sleep 1 && /etc/init.d/uhttpd restart) >/dev/null 2>&1 &
  echo '{"success": true}'
else
  echo '{"success": false, "error": "Failed to change password"}'
fi
