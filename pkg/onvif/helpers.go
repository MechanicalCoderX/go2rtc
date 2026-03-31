package onvif

import (
	"crypto/sha512"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

type DiscoveryDevice struct {
	URL      string
	Name     string
	Hardware string
}

func FindTagValue(b []byte, tag string) string {
	re := regexp.MustCompile(`(?s)<(?:\w+:)?` + tag + `\b[^>]*>([^<]+)`)
	m := re.FindSubmatch(b)
	if len(m) != 2 {
		return ""
	}
	return string(m[1])
}

// UUID - generate something like 44302cbf-0d18-4feb-79b3-33b575263da3
func UUID() string {
	s := core.RandString(32, 16)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// StreamSerial returns a short deterministic hex serial number for a stream,
// used as the ONVIF SerialNumber in GetDeviceInformation responses.
func StreamSerial(name string) string {
	b := sha512.Sum512([]byte(name))
	return fmt.Sprintf("%016x", b[16:24])
}

// StreamUUID returns a deterministic UUID derived from a stream name.
// The same name always produces the same UUID across restarts, giving
// each stream a stable ONVIF endpoint identity.
func StreamUUID(name string) string {
	b := sha512.Sum512([]byte(name))
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// StreamMAC returns a deterministic locally-administered MAC address for a stream.
// The locally-administered bit (0x02) is set in the first octet so it cannot
// collide with real hardware MACs. NVRs (including UniFi Protect) use the MAC
// from GetNetworkInterfaces as the primary unique camera identifier.
func StreamMAC(name string) string {
	b := sha512.Sum512([]byte(name))
	// Set locally-administered bit, clear multicast bit.
	mac0 := (b[24] | 0x02) &^ 0x01
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		mac0, b[25], b[26], b[27], b[28], b[29])
}

// DiscoveryStreamingDevices return list of tuple (onvif_url, name, hardware)
func DiscoveryStreamingDevices() ([]DiscoveryDevice, error) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, err
	}

	defer conn.Close()

	// https://www.onvif.org/wp-content/uploads/2016/12/ONVIF_Feature_Discovery_Specification_16.07.pdf
	// 5.3 Discovery Procedure:
	msg := `<?xml version="1.0" ?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
	<s:Header xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing">
		<a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</a:Action>
		<a:MessageID>urn:uuid:` + UUID() + `</a:MessageID>
		<a:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To>
	</s:Header>
	<s:Body>
		<d:Probe xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
			<d:Types />
			<d:Scopes />
		</d:Probe>
	</s:Body>
</s:Envelope>`

	addr := &net.UDPAddr{
		IP:   net.IP{239, 255, 255, 250},
		Port: 3702,
	}

	if _, err = conn.WriteTo([]byte(msg), addr); err != nil {
		return nil, err
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var devices []DiscoveryDevice

	b := make([]byte, 8192)
	for {
		n, addr, err := conn.ReadFromUDP(b)
		if err != nil {
			break
		}

		//log.Printf("[onvif] discovery response addr=%s:\n%s", addr, b[:n])

		// ignore printers, etc
		if !strings.Contains(string(b[:n]), "onvif") {
			continue
		}

		device := DiscoveryDevice{
			URL: FindTagValue(b[:n], "XAddrs"),
		}

		if device.URL == "" {
			continue
		}

		// fix some buggy cameras
		// <wsdd:XAddrs>http://0.0.0.0:8080/onvif/device_service</wsdd:XAddrs>
		if s, ok := strings.CutPrefix(device.URL, "http://0.0.0.0"); ok {
			device.URL = "http://" + addr.IP.String() + s
		}

		// try to find the camera name and model (hardware)
		scopes := FindTagValue(b[:n], "Scopes")
		device.Name = findScope(scopes, "onvif://www.onvif.org/name/")
		device.Hardware = findScope(scopes, "onvif://www.onvif.org/hardware/")

		devices = append(devices, device)
	}

	return devices, nil
}

func findScope(s, prefix string) string {
	s = core.Between(s, prefix, " ")
	s, _ = url.QueryUnescape(s)
	return s
}

func atoi(s string) int {
	if s == "" {
		return 0
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return i
}

func GetPosixTZ(current time.Time) string {
	// Thanks to https://github.com/Path-Variable/go-posix-time
	_, offset := current.Zone()

	if current.IsDST() {
		_, end := current.ZoneBounds()
		endPlus1 := end.Add(time.Hour * 25)
		_, offset = endPlus1.Zone()
	}

	var prefix string
	if offset < 0 {
		prefix = "GMT+"
		offset = -offset / 60
	} else {
		prefix = "GMT-"
		offset = offset / 60
	}

	return prefix + fmt.Sprintf("%02d:%02d", offset/60, offset%60)
}

func GetPath(urlOrPath, defPath string) string {
	if urlOrPath == "" || urlOrPath[0] == '/' {
		return defPath
	}
	u, err := url.Parse(urlOrPath)
	if err != nil {
		return defPath
	}
	return GetPath(u.Path, defPath)
}
