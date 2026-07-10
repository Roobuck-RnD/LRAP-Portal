package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	IFNAMSIZ = 16

	// MTK / Ralink private ioctl:
	// iwpriv ra0 get_mac_table -> 0x8BEF
	MTK_IOCTL_GET_MAC_TABLE = 0x8BEF

	// get_mac_table 返回结构：
	// offset 0: uint32 station count
	// offset 8: first entry
	// entry size: 40 bytes
	//
	// 根据你设备实际 dump：
	// entry + 1  : MAC, 6 bytes
	// entry + 12 : RSSI chain values, 4 x int8
	// entry + 16 : connected seconds, uint32 little endian
	// entry + 20 : tx rate raw, uint32 little endian
	MTK_MAC_TABLE_HEADER_SIZE = 8
	MTK_MAC_TABLE_ENTRY_SIZE  = 40
	MTK_MAC_TABLE_MAX_SIZE    = 32768
)

type iwPoint struct {
	Pointer uintptr
	Length  uint16
	Flags   uint16
	Pad     [4]byte
}

type iwreq struct {
	Name [IFNAMSIZ]byte
	Data iwPoint
}

type StationInfo struct {
	MAC              string `json:"mac"`
	Interface        string `json:"interface,omitempty"`
	RSSI             int    `json:"rssi"`
	Signal           string `json:"signal"`
	RSSIChains       []int  `json:"rssi_chains,omitempty"`
	ConnectedSeconds int    `json:"connected_seconds"`
	ConnectedTime    string `json:"connected_time"`
	TXRateRaw        string `json:"tx_rate_raw,omitempty"`
	TXRate           string `json:"tx_rate,omitempty"`
}

func main() {
	ifaces := []string{"ra0"}

	if len(os.Args) >= 2 {
		ifaces = nil
		for _, arg := range os.Args[1:] {
			iface := strings.TrimSpace(arg)
			if iface != "" {
				ifaces = append(ifaces, iface)
			}
		}
		if len(ifaces) == 0 {
			ifaces = []string{"ra0"}
		}
	}

	allStations := make([]StationInfo, 0)
	errs := make([]string, 0)

	for _, iface := range ifaces {
		stations, err := getMTKMacTable(iface)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", iface, err))
			continue
		}

		allStations = append(allStations, stations...)
	}

	if len(allStations) == 0 && len(errs) > 0 {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"error": strings.Join(errs, "; "),
		})
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(allStations)
}

func getMTKMacTable(iface string) ([]StationInfo, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket failed: %w", err)
	}
	defer syscall.Close(fd)

	buf := make([]byte, MTK_MAC_TABLE_MAX_SIZE)

	var req iwreq
	copy(req.Name[:], []byte(iface))
	req.Data.Pointer = uintptr(unsafe.Pointer(&buf[0]))
	req.Data.Length = uint16(len(buf))
	req.Data.Flags = 0

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(MTK_IOCTL_GET_MAC_TABLE),
		uintptr(unsafe.Pointer(&req)),
	)

	if errno != 0 {
		return nil, fmt.Errorf("ioctl get_mac_table failed: errno=%d (%s)", errno, errno.Error())
	}

	count := int(binary.LittleEndian.Uint32(buf[0:4]))
	if count < 0 {
		count = 0
	}

	maxCount := (len(buf) - MTK_MAC_TABLE_HEADER_SIZE) / MTK_MAC_TABLE_ENTRY_SIZE
	if count > maxCount {
		count = maxCount
	}

	out := make([]StationInfo, 0, count)

	for i := 0; i < count; i++ {
		base := MTK_MAC_TABLE_HEADER_SIZE + i*MTK_MAC_TABLE_ENTRY_SIZE

		if base+MTK_MAC_TABLE_ENTRY_SIZE > len(buf) {
			break
		}

		macBytes := buf[base+1 : base+7]
		if isZeroMAC(macBytes) {
			continue
		}

		rssiChains := []int{
			int(int8(buf[base+12])),
			int(int8(buf[base+13])),
			int(int8(buf[base+14])),
			int(int8(buf[base+15])),
		}

		rssi := bestRSSI(rssiChains)

		connectedSeconds := int(binary.LittleEndian.Uint32(buf[base+16 : base+20]))
		txRateRaw := binary.LittleEndian.Uint32(buf[base+20 : base+24])

		out = append(out, StationInfo{
			MAC:              formatMAC(macBytes),
			Interface:        iface,
			RSSI:             rssi,
			Signal:           signalText(rssi),
			RSSIChains:       rssiChains,
			ConnectedSeconds: connectedSeconds,
			ConnectedTime:    formatDuration(connectedSeconds),
			TXRateRaw:        fmt.Sprintf("0x%08x", txRateRaw),
			TXRate:           formatRawRate(txRateRaw),
		})
	}

	return out, nil
}

func isZeroMAC(mac []byte) bool {
	if len(mac) != 6 {
		return true
	}

	for _, b := range mac {
		if b != 0x00 {
			return false
		}
	}

	return true
}

func formatMAC(mac []byte) string {
	if len(mac) != 6 {
		return ""
	}

	return strings.ToUpper(fmt.Sprintf(
		"%02x:%02x:%02x:%02x:%02x:%02x",
		mac[0], mac[1], mac[2], mac[3], mac[4], mac[5],
	))
}

func bestRSSI(chains []int) int {
	best := 0

	for _, r := range chains {
		// -109 在你的 dump 里像是无效 chain。
		// 0 也通常不是有效 RSSI。
		if r == 0 || r <= -100 {
			continue
		}

		if best == 0 || r > best {
			best = r
		}
	}

	return best
}

func signalText(rssi int) string {
	if rssi == 0 {
		return "Unknown"
	}
	if rssi >= -55 {
		return "Excellent"
	}
	if rssi >= -67 {
		return "Good"
	}
	if rssi >= -75 {
		return "Fair"
	}
	return "Weak"
}

func formatDuration(seconds int) string {
	if seconds <= 0 {
		return ""
	}

	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}

	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}

	return fmt.Sprintf("%ds", s)
}

func formatRawRate(raw uint32) string {
	if raw == 0 {
		return ""
	}

	// 目前先返回 raw。
	// 后面如果确认 MTK rate bitfield，可以继续解码成：
	// MCS / NSS / BW / GI / HE/VHT/HT。
	return fmt.Sprintf("raw 0x%08x", raw)
}
