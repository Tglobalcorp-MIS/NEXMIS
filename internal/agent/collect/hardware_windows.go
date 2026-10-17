//go:build windows

package collect

import (
	"context"
	"encoding/binary"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

func (c *windowsCollector) Hardware(ctx context.Context) (*protocol.Hardware, error) {
	hw := &protocol.Hardware{}
	ok := false // at least one source succeeded

	// Computer system / VM detection.
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_ComputerSystem", "",
		[]string{"Manufacturer", "Model", "Domain", "PartOfDomain", "TotalPhysicalMemory"}); err == nil && len(rows) > 0 {
		r := rows[0]
		hw.Manufacturer = asString(r["Manufacturer"])
		hw.Model = asString(r["Model"])
		hw.DomainJoined = asBool(r["PartOfDomain"])
		if hw.DomainJoined {
			hw.Domain = asString(r["Domain"]) // otherwise it holds the workgroup name
		}
		hw.MemoryTotal = asUint64(r["TotalPhysicalMemory"])
		ok = true
	} else {
		c.warn("Win32_ComputerSystem", err)
	}

	// BIOS.
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_BIOS", "",
		[]string{"Manufacturer", "SMBIOSBIOSVersion", "ReleaseDate", "SerialNumber"}); err == nil && len(rows) > 0 {
		r := rows[0]
		hw.BIOSVendor = asString(r["Manufacturer"])
		hw.BIOSVersion = asString(r["SMBIOSBIOSVersion"])
		if t, ok := parseCIMDatetime(asString(r["ReleaseDate"])); ok {
			hw.BIOSDate = t.Format("2006-01-02")
		}
		hw.SerialNumber = cleanSerial(asString(r["SerialNumber"]))
		ok = true
	} else {
		c.warn("Win32_BIOS", err)
	}

	// Operating system.
	ov := readOSVersion()
	hw.OSName = ov.Name
	hw.OSEdition = ov.Edition
	hw.OSVersion = ov.DisplayVersion
	hw.OSBuild = ov.BuildString()
	hw.OSArch = osArch()
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_OperatingSystem", "",
		[]string{"Caption", "Version", "BuildNumber", "InstallDate", "OSArchitecture", "TotalVisibleMemorySize"}); err == nil && len(rows) > 0 {
		r := rows[0]
		if hw.OSVersion == "" {
			hw.OSVersion = asString(r["Version"])
		}
		if hw.OSBuild == "" {
			hw.OSBuild = asString(r["BuildNumber"])
		}
		if t, ok := parseCIMDatetime(asString(r["InstallDate"])); ok {
			hw.OSInstallDate = t.Format("2006-01-02")
		}
		if hw.OSArch == "" {
			hw.OSArch = asString(r["OSArchitecture"])
		}
		if hw.OSName == "Windows" {
			hw.OSName = windowsOSName(strings.TrimPrefix(asString(r["Caption"]), "Microsoft "), ov.Build)
		}
		if hw.MemoryTotal == 0 {
			hw.MemoryTotal = asUint64(r["TotalVisibleMemorySize"]) * 1024
		}
		ok = true
	} else {
		c.warn("Win32_OperatingSystem", err)
	}
	if hw.OSInstallDate == "" {
		if v, found := regInt(registry.LOCAL_MACHINE, regCurrentVersion, "InstallDate"); found && v > 0 {
			hw.OSInstallDate = unixDate(int64(v))
		}
	}

	// Chassis.
	var chassis []uint64
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_SystemEnclosure", "", []string{"ChassisTypes", "SerialNumber"}); err == nil {
		for _, r := range rows {
			chassis = append(chassis, asUintSlice(r["ChassisTypes"])...)
			if hw.SerialNumber == "" {
				hw.SerialNumber = cleanSerial(asString(r["SerialNumber"]))
			}
		}
		ok = true
	} else {
		c.warn("Win32_SystemEnclosure", err)
	}
	hw.ChassisType = chassisKind(chassis, hw.Manufacturer, hw.Model)

	// CPUs.
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_Processor", "",
		[]string{"Name", "NumberOfCores", "NumberOfLogicalProcessors", "MaxClockSpeed"}); err == nil {
		for _, r := range rows {
			hw.CPUs = append(hw.CPUs, protocol.CPU{
				Name:         asString(r["Name"]),
				Cores:        asInt(r["NumberOfCores"]),
				LogicalProcs: asInt(r["NumberOfLogicalProcessors"]),
				MaxClockMHz:  asInt(r["MaxClockSpeed"]),
			})
		}
		ok = true
	} else {
		c.warn("Win32_Processor", err)
	}

	// Memory modules.
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_PhysicalMemory", "",
		[]string{"BankLabel", "DeviceLocator", "Capacity", "Speed", "ConfiguredClockSpeed", "Manufacturer", "PartNumber"}); err == nil {
		for _, r := range rows {
			slot := asString(r["DeviceLocator"])
			if slot == "" {
				slot = asString(r["BankLabel"])
			}
			speed := asInt(r["ConfiguredClockSpeed"])
			if speed == 0 {
				speed = asInt(r["Speed"])
			}
			hw.MemoryModules = append(hw.MemoryModules, protocol.MemModule{
				Slot:         slot,
				Capacity:     asUint64(r["Capacity"]),
				SpeedMHz:     speed,
				Manufacturer: asString(r["Manufacturer"]),
				PartNumber:   asString(r["PartNumber"]),
			})
		}
		ok = true
	} else {
		c.warn("Win32_PhysicalMemory", err)
	}
	if hw.MemoryTotal == 0 {
		if total, _, err := globalMemory(); err == nil {
			hw.MemoryTotal = total
		}
	}

	// Physical disks.
	hw.Disks = c.physicalDisks(ctx)

	// Volumes.
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_LogicalDisk", "DriveType=3",
		[]string{"DeviceID", "VolumeName", "FileSystem", "Size", "FreeSpace"}); err == nil {
		for _, r := range rows {
			hw.Volumes = append(hw.Volumes, protocol.Volume{
				Mount:      asString(r["DeviceID"]),
				Label:      asString(r["VolumeName"]),
				FileSystem: asString(r["FileSystem"]),
				Size:       asUint64(r["Size"]),
				Free:       asUint64(r["FreeSpace"]),
			})
		}
		ok = true
	} else {
		c.warn("Win32_LogicalDisk", err)
		hw.Volumes = fixedVolumes()
	}
	sort.Slice(hw.Volumes, func(i, j int) bool { return hw.Volumes[i].Mount < hw.Volumes[j].Mount })

	// NICs.
	if ads, err := adapters(); err == nil {
		for _, a := range ads {
			if a.IsLoopback || a.IsTunnel || !a.HasHardware {
				continue
			}
			name := a.Name
			if name == "" {
				name = a.FriendlyNm
			}
			hw.NICs = append(hw.NICs, protocol.NIC{
				Name:      name,
				MAC:       a.MAC,
				IPs:       a.IPs,
				Gateway:   a.Gateways,
				DNS:       a.DNS,
				DHCP:      a.DHCP,
				SpeedMbps: a.SpeedMbps,
				Type:      a.Type,
			})
		}
		ok = true
	} else {
		c.warn("GetAdaptersAddresses", err)
	}

	// GPUs.
	if rows, err := wmiQuery(ctx, nsCIMV2, "Win32_VideoController", "",
		[]string{"Name", "DriverVersion", "AdapterRAM"}); err == nil {
		vram := gpuMemoryFromRegistry()
		for _, r := range rows {
			g := protocol.GPU{
				Name:          asString(r["Name"]),
				DriverVersion: asString(r["DriverVersion"]),
				VRAM:          asUint64(r["AdapterRAM"]), // uint32: capped at 4 GiB
			}
			if v := vram[strings.ToLower(g.Name)]; v > g.VRAM {
				g.VRAM = v
			}
			hw.GPUs = append(hw.GPUs, g)
		}
		ok = true
	} else {
		c.warn("Win32_VideoController", err)
	}

	// Monitors (EDID via WmiMonitorID).
	if rows, err := wmiQuery(ctx, nsWMI, "WmiMonitorID", "",
		[]string{"ManufacturerName", "UserFriendlyName", "SerialNumberID", "ProductCodeID"}); err == nil {
		for _, r := range rows {
			m := protocol.Monitor{
				Manufacturer: utf16ArrayString(r["ManufacturerName"]),
				Model:        utf16ArrayString(r["UserFriendlyName"]),
				SerialNumber: utf16ArrayString(r["SerialNumberID"]),
			}
			if m.Model == "" {
				m.Model = utf16ArrayString(r["ProductCodeID"])
			}
			if m.SerialNumber == "0" {
				m.SerialNumber = ""
			}
			hw.Monitors = append(hw.Monitors, m)
		}
	} else {
		c.warn("WmiMonitorID", err) // normal on headless / some VMs
	}

	// TPM.
	if rows, err := wmiQuery(ctx, nsTPM, "Win32_Tpm", "",
		[]string{"SpecVersion", "IsEnabled_InitialValue", "IsActivated_InitialValue"}); err == nil {
		if len(rows) > 0 {
			hw.TPMPresent = true
			spec := asString(rows[0]["SpecVersion"])
			if i := strings.IndexByte(spec, ','); i >= 0 {
				spec = spec[:i]
			}
			hw.TPMVersion = strings.TrimSpace(spec)
		}
	} else {
		c.warn("Win32_Tpm", err) // namespace missing when there is no TPM
	}

	// Secure Boot.
	if v, found := regInt(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\SecureBoot\State`, "UEFISecureBootEnabled"); found {
		hw.SecureBoot = v == 1
	}

	// BitLocker (namespace absent on Home editions).
	if rows, err := wmiQuery(ctx, nsBitLocker, "Win32_EncryptableVolume", "",
		[]string{"DriveLetter", "ProtectionStatus"}); err == nil {
		for _, r := range rows {
			letter := asString(r["DriveLetter"])
			if letter == "" {
				continue
			}
			status := "Unknown"
			if v, present := r["ProtectionStatus"]; present {
				switch asUint64(v) {
				case 0:
					status = "Off"
				case 1:
					status = "On"
				}
			}
			hw.BitLocker = append(hw.BitLocker, protocol.BitLocker{Mount: letter, ProtectionStatus: status})
		}
		sort.Slice(hw.BitLocker, func(i, j int) bool { return hw.BitLocker[i].Mount < hw.BitLocker[j].Mount })
	} else {
		c.log.Debug("BitLocker WMI unavailable (expected on Home editions)", "err", err)
	}

	if !ok && hw.OSName == "" {
		return hw, errWMIUnresponsive
	}
	return hw, nil
}

// physicalDisks prefers MSFT_PhysicalDisk (media/bus type, health) and falls
// back to Win32_DiskDrive.
func (c *windowsCollector) physicalDisks(ctx context.Context) []protocol.Disk {
	var disks []protocol.Disk
	rows, err := wmiQuery(ctx, nsStorage, "MSFT_PhysicalDisk", "",
		[]string{"FriendlyName", "Model", "SerialNumber", "Size", "MediaType", "BusType", "HealthStatus"})
	if err == nil && len(rows) > 0 {
		for _, r := range rows {
			model := asString(r["Model"])
			if model == "" {
				model = asString(r["FriendlyName"])
			}
			health := "Unknown"
			if v, present := r["HealthStatus"]; present {
				health = diskHealth(asUint64(v))
			}
			disks = append(disks, protocol.Disk{
				Model:        model,
				SerialNumber: cleanSerial(asString(r["SerialNumber"])),
				Size:         asUint64(r["Size"]),
				MediaType:    diskMediaType(asUint64(r["MediaType"])),
				BusType:      diskBusType(asUint64(r["BusType"])),
				Health:       health,
			})
		}
		return disks
	}
	c.warn("MSFT_PhysicalDisk", err)

	rows, err = wmiQuery(ctx, nsCIMV2, "Win32_DiskDrive", "",
		[]string{"Model", "SerialNumber", "Size", "InterfaceType", "MediaType", "Status"})
	if err != nil {
		c.warn("Win32_DiskDrive", err)
		return nil
	}
	for _, r := range rows {
		disks = append(disks, protocol.Disk{
			Model:        asString(r["Model"]),
			SerialNumber: cleanSerial(asString(r["SerialNumber"])),
			Size:         asUint64(r["Size"]),
			MediaType:    "Unspecified",
			BusType:      asString(r["InterfaceType"]),
			Health:       diskDriveHealth(asString(r["Status"])),
		})
	}
	return disks
}

// fixedVolumes enumerates fixed drives with plain Win32 calls (cheap; used
// by Health and as the Win32_LogicalDisk fallback).
func fixedVolumes() []protocol.Volume {
	buf := make([]uint16, 256)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil || n == 0 {
		return nil
	}
	if int(n) > len(buf) {
		buf = make([]uint16, n+1)
		if n, err = windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0]); err != nil || int(n) > len(buf) {
			return nil
		}
	}
	var out []protocol.Volume
	for _, root := range strings.Split(windows.UTF16ToString(nullsToSep(buf[:n])), "|") {
		if root == "" {
			continue
		}
		p := utf16Ptr(root)
		if p == nil || windows.GetDriveType(p) != windows.DRIVE_FIXED {
			continue
		}
		var freeAvail, total, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(p, &freeAvail, &total, &totalFree); err != nil {
			continue
		}
		label := make([]uint16, windows.MAX_PATH+1)
		fs := make([]uint16, windows.MAX_PATH+1)
		v := protocol.Volume{Mount: strings.TrimSuffix(root, `\`), Size: total, Free: totalFree}
		if err := windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, &fs[0], uint32(len(fs))); err == nil {
			v.Label = windows.UTF16ToString(label)
			v.FileSystem = windows.UTF16ToString(fs)
		}
		out = append(out, v)
	}
	return out
}

// nullsToSep converts a REG_MULTI_SZ-style double-NUL list into a single
// "|"-separated string so it can be split.
func nullsToSep(b []uint16) []uint16 {
	out := make([]uint16, len(b))
	copy(out, b)
	for i, c := range out {
		if c == 0 {
			out[i] = '|'
		}
	}
	return out
}

// gpuMemoryFromRegistry maps lower-case DriverDesc to the dedicated VRAM
// size from the display adapter class key (Win32_VideoController.AdapterRAM
// is a uint32 and wraps above 4 GiB).
func gpuMemoryFromRegistry() map[string]uint64 {
	const classKey = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	out := map[string]uint64{}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, classKey, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return out
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	for _, n := range names {
		if len(n) != 4 || !isDigits(n) {
			continue
		}
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		desc := keyString(sk, "DriverDesc")
		var size uint64
		if v, ok := keyInt(sk, "HardwareInformation.qwMemorySize"); ok {
			size = v
		} else if b, _, err := sk.GetBinaryValue("HardwareInformation.qwMemorySize"); err == nil && len(b) >= 8 {
			size = binary.LittleEndian.Uint64(b)
		} else if v, ok := keyInt(sk, "HardwareInformation.MemorySize"); ok {
			size = v
		} else if b, _, err := sk.GetBinaryValue("HardwareInformation.MemorySize"); err == nil && len(b) >= 4 {
			size = uint64(binary.LittleEndian.Uint32(b))
		}
		sk.Close()
		if desc != "" && size > 0 {
			out[strings.ToLower(desc)] = size
		}
	}
	return out
}
