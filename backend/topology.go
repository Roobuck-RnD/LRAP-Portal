package main

// topology.go 是设备拓扑与常量的唯一事实源。所有 AP 管理 IP、AC IP、匿名 SID
// 都从这里取,不要在其它文件里再写字面量,避免改拓扑/换网段时漏改。

// AnonSID 是 AC 对 AP 发起匿名(未认证)ubus 调用时使用的零 SID。AP 侧的 rpcd
// unauthenticated ACL 授权这个 SID。唯一定义处。
const AnonSID = "00000000000000000000000000000000"

// ACManagementIP 是主模块(AC)的固定管理 IP。
const ACManagementIP = "10.10.18.1"

// apManagementIPs 是各子模块(AP)的固定管理 IP。AC LAN 口接 AP WAN 口,
// AP 使用这些静态地址。整套绑定在 10.10.18.0/24 管理网。
var apManagementIPs = []string{
	"10.10.18.2",
	"10.10.18.3",
	"10.10.18.4",
	"10.10.18.5",
}

// APManagementIPs 返回受管 AP 的 IP 列表(副本,防止调用方修改内部切片)。
func APManagementIPs() []string {
	out := make([]string, len(apManagementIPs))
	copy(out, apManagementIPs)
	return out
}

// IsManagedAPIP 判断给定 IP 是否是受管 AP 的管理地址。
func IsManagedAPIP(ip string) bool {
	for _, a := range apManagementIPs {
		if a == ip {
			return true
		}
	}
	return false
}
