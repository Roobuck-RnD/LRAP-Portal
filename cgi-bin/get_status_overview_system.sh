#!/bin/sh
echo "Content-Type: application/json"
echo ""

# 从 QUERY_STRING 中解析参数
query="$QUERY_STRING"

ip=$(echo "$query" | sed -n 's/.*ip=\([^&]*\).*/\1/p')
module_type=$(echo "$query" | sed -n 's/.*moduleType=\([^&]*\).*/\1/p' | sed 's/%20/ /g; s/+/ /g')


user="root"
pass="admin"

# === 本地信息函数 ===
get_local_sysinfo() {
  hostname=$(uci get system.@system[0].hostname 2>/dev/null)
  [ -z "$hostname" ] && hostname=$(hostname)

  model=$(cat /proc/device-tree/model 2>/dev/null)
  architecture=$(uname -m)
  target=$(grep 'DISTRIB_TARGET=' /etc/openwrt_release | cut -d"'" -f2)
  firmware_version=$(cat /etc/openwrt_version)
  kernel_version=$(uname -r)
  local_time=$(date +"%Y-%m-%d %H:%M:%S")
  uptime=$(awk '{ up=$1; days=int(up/86400); hrs=int((up%86400)/3600); mins=int((up%3600)/60); printf "%d days, %02d hours, %02d minutes", days, hrs, mins }' /proc/uptime)
  loadavg=$(cut -d ' ' -f1-3 /proc/loadavg)
  temperature=$(awk '{ printf "%.1f°C", $1 / 1000 }' /sys/class/thermal/thermal_zone0/temp 2>/dev/null)

  echo "{
    \"hostname\": \"$hostname\",
    \"model\": \"${model:-unknown}\",
    \"architecture\": \"$architecture\",
    \"target\": \"$target\",
    \"firmware_version\": \"$firmware_version\",
    \"kernel_version\": \"$kernel_version\",
    \"local_time\": \"$local_time\",
    \"uptime\": \"$uptime\",
    \"load_average\": \"$loadavg\",
    \"temperature\": \"$temperature\"
  }"
}

# === 子模块信息函数（远程调用） ===
get_remote_sysinfo() {
  sshpass -p "$pass" ssh -o ConnectTimeout=3 -y "$user@$ip" 'sh -s' <<'EOF'
hostname=$(uci get system.@system[0].hostname 2>/dev/null)
[ -z "$hostname" ] && hostname=$(hostname)

model=$(cat /proc/device-tree/model 2>/dev/null)
architecture=$(uname -m)
target=$(grep 'DISTRIB_TARGET=' /etc/openwrt_release | cut -d"'" -f2)
firmware_version=$(cat /etc/openwrt_version)
kernel_version=$(uname -r)
local_time=$(date +"%Y-%m-%d %H:%M:%S")
uptime=$(awk '{ up=$1; days=int(up/86400); hrs=int((up%86400)/3600); mins=int((up%3600)/60); printf "%d days, %02d hours, %02d minutes", days, hrs, mins }' /proc/uptime)
loadavg=$(cut -d ' ' -f1-3 /proc/loadavg)
temperature=$(awk '{ printf "%.1f°C", $1 / 1000 }' /sys/class/thermal/thermal_zone0/temp 2>/dev/null)

echo "{
  \"hostname\": \"$hostname\",
  \"model\": \"${model:-unknown}\",
  \"architecture\": \"$architecture\",
  \"target\": \"$target\",
  \"firmware_version\": \"$firmware_version\",
  \"kernel_version\": \"$kernel_version\",
  \"local_time\": \"$local_time\",
  \"uptime\": \"$uptime\",
  \"load_average\": \"$loadavg\",
  \"temperature\": \"$temperature\"
}"
EOF
}

# === 判断模块类型并执行 ===
if [ "$module_type" = "Main Module" ]; then
  get_local_sysinfo
else
  get_remote_sysinfo
fi
