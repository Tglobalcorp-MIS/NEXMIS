//go:build windows

package collect

import (
	"context"
	"errors"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
)

const uninstallPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

// Software reads the Uninstall registry keys. Win32_Product is deliberately
// not used: enumerating it triggers MSI consistency checks/repairs and takes
// minutes.
func (c *windowsCollector) Software(ctx context.Context) ([]protocol.Software, error) {
	arch := osArch()
	native := "x64"
	if arch == "arm64" {
		native = "arm64"
	}
	is64 := arch != "x86"

	type source struct {
		root  registry.Key
		path  string
		view  uint32
		arch  string
		scope string
	}
	var sources []source
	if is64 {
		sources = append(sources,
			source{registry.LOCAL_MACHINE, uninstallPath, registry.WOW64_64KEY, native, "machine"},
			source{registry.LOCAL_MACHINE, uninstallPath, registry.WOW64_32KEY, "x86", "machine"})
	} else {
		sources = append(sources, source{registry.LOCAL_MACHINE, uninstallPath, 0, "x86", "machine"})
	}
	for _, sid := range loadedUserSIDs() {
		// HKCU\Software is shared between views; architecture is unknown.
		sources = append(sources, source{registry.USERS, sid + `\` + uninstallPath, 0, "", "user"})
	}

	seen := map[string]bool{}
	var out []protocol.Software
	anyOK := false
	for _, s := range sources {
		if ctx.Err() != nil {
			break
		}
		items, err := readUninstallKey(s.root, s.path, s.view, s.arch, s.scope)
		if err != nil {
			if s.scope == "machine" {
				c.warn("uninstall "+s.path, err)
			}
			continue
		}
		anyOK = true
		for _, it := range items {
			key := strings.ToLower(it.Name) + "\x00" + strings.ToLower(it.Version) + "\x00" + it.Arch
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].Version < out[j].Version
	})
	if out == nil {
		out = []protocol.Software{}
	}
	if !anyOK {
		return out, errors.New("collect: no uninstall registry key readable")
	}
	return out, nil
}

// loadedUserSIDs lists real user hives currently loaded under HKU (local and
// domain users S-1-5-21-*, Entra ID users S-1-12-1-*).
func loadedUserSIDs() []string {
	k, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	var out []string
	for _, n := range names {
		if strings.HasSuffix(strings.ToLower(n), "_classes") {
			continue
		}
		if strings.HasPrefix(n, "S-1-5-21-") || strings.HasPrefix(n, "S-1-12-1-") {
			out = append(out, n)
		}
	}
	return out
}

func readUninstallKey(root registry.Key, path string, view uint32, arch, scope string) ([]protocol.Software, error) {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE|view)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	var out []protocol.Software
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE|view)
		if err != nil {
			continue
		}
		name := keyString(sk, "DisplayName")
		sysComp, _ := keyInt(sk, "SystemComponent")
		parent := keyString(sk, "ParentKeyName")
		release := strings.ToLower(keyString(sk, "ReleaseType"))
		if name == "" || sysComp == 1 || parent != "" ||
			release == "security update" || release == "update rollup" || release == "hotfix" {
			sk.Close()
			continue
		}
		out = append(out, protocol.Software{
			Name:        name,
			Version:     keyString(sk, "DisplayVersion"),
			Publisher:   keyString(sk, "Publisher"),
			InstallDate: normalizeInstallDate(keyString(sk, "InstallDate")),
			Arch:        arch,
			Scope:       scope,
			UninstallID: n,
		})
		sk.Close()
	}
	return out, nil
}
