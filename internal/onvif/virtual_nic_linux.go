//go:build linux

package onvif

import (
	"fmt"
	"net"
	"os/exec"
	"strings"

	pkgonvif "github.com/AlexxIT/go2rtc/pkg/onvif"
)

// ensureVirtualNIC creates a macvlan virtual network interface for the given
// stream, assigning it the stream's deterministic MAC (StreamMAC) and the
// configured IP address. The returned cleanup function removes the interface.
//
// Requires CAP_NET_ADMIN (standard in Docker with cap_add: NET_ADMIN and
// in privileged Proxmox LXC containers). The parent interface is detected
// automatically as the first non-loopback interface whose subnet contains
// the target IP.
func ensureVirtualNIC(streamName, targetIP string) (func(), error) {
	parent, prefix, err := findParentInterface(targetIP)
	if err != nil {
		return nil, err
	}

	ifaceName := virtualIfaceName(streamName)
	mac := pkgonvif.StreamMAC(streamName)
	cidr := fmt.Sprintf("%s/%d", targetIP, prefix)

	if out, err := exec.Command("ip", "link", "add", ifaceName,
		"link", parent, "type", "macvlan", "mode", "bridge",
		"address", mac).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ip link add: %w: %s", err, out)
	}

	if out, err := exec.Command("ip", "addr", "add", cidr, "dev", ifaceName).CombinedOutput(); err != nil {
		exec.Command("ip", "link", "delete", ifaceName).Run()
		return nil, fmt.Errorf("ip addr add: %w: %s", err, out)
	}

	if out, err := exec.Command("ip", "link", "set", ifaceName, "up").CombinedOutput(); err != nil {
		exec.Command("ip", "link", "delete", ifaceName).Run()
		return nil, fmt.Errorf("ip link set up: %w: %s", err, out)
	}

	log.Info().Str("stream", streamName).Str("ip", targetIP).
		Str("mac", mac).Str("iface", ifaceName).Msg("[onvif] created virtual NIC")

	return func() {
		if err := exec.Command("ip", "link", "delete", ifaceName).Run(); err != nil {
			log.Debug().Err(err).Str("iface", ifaceName).Msg("[onvif] remove virtual NIC")
		} else {
			log.Debug().Str("iface", ifaceName).Msg("[onvif] removed virtual NIC")
		}
	}, nil
}

// cleanupOrphanedVirtualNICs removes any go2rtc_* interfaces left over from a
// previous run that did not exit cleanly. Called at startup before creating
// new interfaces.
func cleanupOrphanedVirtualNICs() {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, "go2rtc_") {
			if err := exec.Command("ip", "link", "delete", iface.Name).Run(); err != nil {
				log.Debug().Err(err).Str("iface", iface.Name).Msg("[onvif] cleanup orphaned NIC")
			} else {
				log.Debug().Str("iface", iface.Name).Msg("[onvif] removed orphaned NIC")
			}
		}
	}
}

// findParentInterface returns the name and prefix length of the first
// non-loopback, up interface whose subnet contains targetIP.
func findParentInterface(targetIP string) (name string, prefixLen int, err error) {
	target := net.ParseIP(targetIP)
	if target == nil {
		return "", 0, fmt.Errorf("invalid IP address: %s", targetIP)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", 0, err
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			if ipnet.Contains(target) {
				ones, _ := ipnet.Mask.Size()
				return iface.Name, ones, nil
			}
		}
	}

	return "", 0, fmt.Errorf("no interface found in same subnet as %s", targetIP)
}

// virtualIfaceName converts a stream name to a valid Linux interface name.
// Interface names are limited to 15 characters; "go2rtc_" is 7, leaving 8
// for the (sanitised) stream name.
func virtualIfaceName(streamName string) string {
	var b strings.Builder
	b.WriteString("go2rtc_")
	for _, r := range strings.ToLower(streamName) {
		if b.Len() >= 15 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
