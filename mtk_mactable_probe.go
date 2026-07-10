package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	IFNAMSIZ = 16

	// From your iwpriv list:
	// get_mac_table (8BEF)
	// show          (8BF1)
	// stat          (8BE9)
	MTK_IOCTL_GET_MAC_TABLE = 0x8BEF
	MTK_IOCTL_SHOW          = 0x8BF1
	MTK_IOCTL_STAT          = 0x8BE9
)

// aarch64 Linux struct iw_point inside struct iwreq:
// pointer: 8 bytes
// length:  uint16
// flags:   uint16
// padding: 4 bytes
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

func setIfaceName(req *iwreq, ifname string) {
	copy(req.Name[:], []byte(ifname))
}

func printableDump(buf []byte) string {
	var b strings.Builder

	for _, c := range buf {
		switch {
		case c == 0:
			b.WriteByte('.')
		case c >= 32 && c <= 126:
			b.WriteByte(c)
		case c == '\n' || c == '\r' || c == '\t':
			b.WriteByte(c)
		default:
			b.WriteByte('.')
		}
	}

	return b.String()
}

func hexDump(buf []byte, max int) string {
	if len(buf) > max {
		buf = buf[:max]
	}

	return hex.Dump(buf)
}

func callDataIoctl(ifname string, cmd uintptr, label string, buflen int) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		fmt.Printf("socket failed: %v\n", err)
		return
	}
	defer syscall.Close(fd)

	buf := make([]byte, buflen)

	var req iwreq
	setIfaceName(&req, ifname)
	req.Data.Pointer = uintptr(unsafe.Pointer(&buf[0]))
	req.Data.Length = uint16(buflen)
	req.Data.Flags = 0

	fmt.Println()
	fmt.Println("==============================")
	fmt.Printf("CALL %s cmd=0x%x buflen=%d iface=%s\n", label, cmd, buflen, ifname)
	fmt.Println("==============================")

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		cmd,
		uintptr(unsafe.Pointer(&req)),
	)

	if errno != 0 {
		fmt.Printf("ioctl failed: errno=%d (%s)\n", errno, errno.Error())
		return
	}

	length := int(req.Data.Length)
	if length <= 0 || length > buflen {
		length = buflen
	}

	if length > 4096 {
		length = 4096
	}

	out := buf[:length]

	fmt.Printf("ioctl ok. returned length=%d flags=%d\n", req.Data.Length, req.Data.Flags)

	fmt.Println("----- ASCII-ish dump -----")
	fmt.Println(printableDump(out))

	fmt.Printf("----- HEX dump first %d bytes -----\n", len(out))
	fmt.Print(hexDump(out, len(out)))
}

func callShowIoctl(ifname string, param string) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		fmt.Printf("socket failed: %v\n", err)
		return
	}
	defer syscall.Close(fd)

	buf := make([]byte, 1024)
	copy(buf, []byte(param))

	var req iwreq
	setIfaceName(&req, ifname)
	req.Data.Pointer = uintptr(unsafe.Pointer(&buf[0]))
	req.Data.Length = uint16(len(param) + 1)
	req.Data.Flags = 0

	fmt.Println()
	fmt.Println("==============================")
	fmt.Printf("CALL show param=%s iface=%s\n", param, ifname)
	fmt.Println("==============================")

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(MTK_IOCTL_SHOW),
		uintptr(unsafe.Pointer(&req)),
	)

	if errno != 0 {
		fmt.Printf("show ioctl failed: errno=%d (%s)\n", errno, errno.Error())
		return
	}

	length := int(req.Data.Length)
	if length <= 0 || length > len(buf) {
		length = len(buf)
	}

	fmt.Printf("show ioctl ok. len=%d flags=%d\n", req.Data.Length, req.Data.Flags)
	fmt.Println("----- ASCII-ish dump -----")
	fmt.Println(printableDump(buf[:length]))
	fmt.Println("----- HEX dump -----")
	fmt.Print(hexDump(buf[:length], 1024))
}

func main() {
	ifname := "ra0"
	if len(os.Args) >= 2 {
		ifname = os.Args[1]
	}

	fmt.Printf("MTK mac table ioctl probe for iface=%s\n", ifname)

	callDataIoctl(ifname, MTK_IOCTL_GET_MAC_TABLE, "get_mac_table", 1024)
	callDataIoctl(ifname, MTK_IOCTL_GET_MAC_TABLE, "get_mac_table", 2048)
	callDataIoctl(ifname, MTK_IOCTL_GET_MAC_TABLE, "get_mac_table", 4096)
	callDataIoctl(ifname, MTK_IOCTL_GET_MAC_TABLE, "get_mac_table", 8192)

	callShowIoctl(ifname, "stainfo")
	callShowIoctl(ifname, "stacountinfo")
	callShowIoctl(ifname, "stasecinfo")
	callShowIoctl(ifname, "bainfo")

	callDataIoctl(ifname, MTK_IOCTL_STAT, "stat", 2048)
}