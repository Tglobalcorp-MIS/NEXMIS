package discovery

import "strings"

// ouiVendors maps the first three MAC octets to a vendor for the device
// families commonly found on office networks. It is intentionally small;
// unknown prefixes simply leave the vendor empty.
var ouiVendors = map[string]string{
	// Printers
	"00:00:48": "Epson", "00:26:ab": "Epson", "64:eb:8c": "Epson", "a4:ee:57": "Epson", "38:1a:52": "Epson",
	"00:00:85": "Canon", "00:1e:8f": "Canon", "88:87:17": "Canon", "f4:81:39": "Canon", "18:0c:ac": "Canon", "c4:ac:59": "Canon",
	"00:80:77": "Brother", "00:1b:a9": "Brother", "30:05:5c": "Brother", "3c:2a:f4": "Brother", "b4:22:00": "Brother",
	"00:00:74": "Ricoh", "00:26:73": "Ricoh", "58:38:79": "Ricoh", "00:17:c8": "Kyocera", "00:c0:ee": "Kyocera",
	"00:00:aa": "Xerox", "9c:93:4e": "Xerox", "00:04:00": "Lexmark", "00:21:b7": "Lexmark", "00:20:6b": "Konica Minolta",
	"00:22:f3": "Sharp", "00:26:09": "Fuji Xerox",
	"00:1b:78": "HP", "3c:d9:2b": "HP", "9c:8e:99": "HP", "a0:d3:c1": "HP", "ec:b1:d7": "HP", "10:1f:74": "HP", "94:57:a5": "HP",
	// Network
	"00:1a:a1": "Cisco", "00:1b:54": "Cisco", "00:25:45": "Cisco", "58:97:bd": "Cisco", "70:10:5c": "Cisco", "f0:29:29": "Cisco",
	"00:0b:86": "Aruba", "24:de:c6": "Aruba", "94:b4:0f": "Aruba", "00:05:85": "Juniper", "28:8a:1c": "Juniper",
	"00:15:6d": "Ubiquiti", "04:18:d6": "Ubiquiti", "24:a4:3c": "Ubiquiti", "74:83:c2": "Ubiquiti", "f0:9f:c2": "Ubiquiti", "78:8a:20": "Ubiquiti",
	"4c:5e:0c": "MikroTik", "64:d1:54": "MikroTik", "b8:69:f4": "MikroTik", "cc:2d:e0": "MikroTik",
	"14:cc:20": "TP-Link", "50:c7:bf": "TP-Link", "98:da:c4": "TP-Link", "c0:4a:00": "TP-Link", "f4:f2:6d": "TP-Link",
	"00:13:49": "Zyxel", "5c:f4:ab": "Zyxel", "00:09:0f": "Fortinet", "90:6c:ac": "Fortinet", "70:4c:a5": "Fortinet",
	"00:14:6c": "Netgear", "a0:40:a0": "Netgear", "00:05:5d": "D-Link", "1c:7e:e5": "D-Link",
	// Storage
	"00:11:32": "Synology", "90:09:d0": "Synology", "00:08:9b": "QNAP", "24:5e:be": "QNAP",
	// Computers / NICs
	"00:14:22": "Dell", "18:db:f2": "Dell", "b8:ca:3a": "Dell", "f8:bc:12": "Dell", "d4:81:d7": "Dell",
	"00:21:cc": "Lenovo", "54:ee:75": "Lenovo", "8c:16:45": "Lenovo", "e8:6a:64": "Lenovo",
	"00:1b:21": "Intel", "3c:a9:f4": "Intel", "a4:4e:31": "Intel", "f8:63:3f": "Intel",
	"00:e0:4c": "Realtek", "52:54:00": "QEMU/KVM", "00:50:56": "VMware", "00:0c:29": "VMware", "00:15:5d": "Microsoft Hyper-V",
	"08:00:27": "VirtualBox", "b8:27:eb": "Raspberry Pi", "dc:a6:32": "Raspberry Pi",
	"00:1c:42": "Parallels", "f0:18:98": "Apple", "a4:83:e7": "Apple", "3c:22:fb": "Apple",
}

// VendorOf returns the vendor for a MAC address, or "".
func VendorOf(mac string) string {
	mac = strings.ToLower(strings.ReplaceAll(mac, "-", ":"))
	if len(mac) < 8 {
		return ""
	}
	return ouiVendors[mac[:8]]
}
