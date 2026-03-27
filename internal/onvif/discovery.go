package onvif

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"

	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/onvif"
)

const (
	wsDiscoveryAddr = "239.255.255.250:3702"
	wsDiscoveryPort = 3702
)

// StartDiscovery listens on the WS-Discovery multicast group and responds to
// Probe messages with a ProbeMatch for each configured go2rtc stream, making
// each stream appear as an independent ONVIF camera to NVRs and clients.
func StartDiscovery() {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// Allow multiple listeners to share port 3702 (e.g. alongside other ONVIF services).
				_ = setsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)

				// Join WS-Discovery multicast group on all interfaces.
				mreq := &syscall.IPMreq{
					Multiaddr: [4]byte{239, 255, 255, 250},
				}
				_ = setsockoptIPMreq(fd, syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)

				ifaces, err := net.Interfaces()
				if err != nil {
					return
				}
				for _, iface := range ifaces {
					if iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagUp == 0 {
						continue
					}
					addrs, err := iface.Addrs()
					if err != nil {
						continue
					}
					for _, addr := range addrs {
						var ip net.IP
						switch v := addr.(type) {
						case *net.IPNet:
							ip = v.IP
						case *net.IPAddr:
							ip = v.IP
						}
						if ip4 := ip.To4(); ip4 != nil && !ip.IsLoopback() {
							mreq.Interface = [4]byte(ip4)
							_ = setsockoptIPMreq(fd, syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
						}
					}
				}
			})
		},
	}

	pc, err := lc.ListenPacket(context.Background(), "udp4", ":3702")
	if err != nil {
		log.Warn().Err(err).Msg("[onvif] discovery server failed to bind; WS-Discovery disabled")
		return
	}

	log.Info().Msgf("[onvif] WS-Discovery listening on %s", wsDiscoveryAddr)

	go serveDiscovery(pc.(*net.UDPConn))
}

func serveDiscovery(conn *net.UDPConn) {
	buf := make([]byte, 8192)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Debug().Err(err).Msg("[onvif] discovery read error")
			return
		}

		packet := buf[:n]
		log.Trace().Msgf("[onvif] discovery probe from %s:\n%s", addr, packet)

		// Only handle WS-Discovery Probe messages.
		if !strings.Contains(string(packet), "Probe") {
			continue
		}

		msgID := onvif.FindTagValue(packet, "MessageID")
		if msgID == "" {
			continue
		}

		// Only advertise streams that have a dedicated IP configured.
		// Streams without ip: are not advertised via WS-Discovery.
		var names []string
		for _, name := range streams.GetAllNames() {
			if ov, ok := streamOverrides[name]; ok && ov.IP != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}

		for _, name := range names {
			resp := buildProbeMatch(name, msgID)
			log.Trace().Msgf("[onvif] discovery ProbeMatch stream=%s to %s", name, addr)
			if _, err = conn.WriteTo([]byte(resp), addr); err != nil {
				log.Debug().Err(err).Str("stream", name).Msg("[onvif] discovery write")
			}
		}
	}
}

// buildProbeMatch returns a WS-Discovery ProbeMatch SOAP envelope for one stream.
// Only called for streams that have ip: configured in streamOverrides.
func buildProbeMatch(name, relatesTo string) string {
	uuid := onvif.StreamUUID(name)

	ov := streamOverrides[name]
	xaddrStr := fmt.Sprintf("http://%s:%d/onvif/device_service", ov.IP, onvifPort)

	return `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
            xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <s:Header>
    <a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</a:Action>
    <a:MessageID>urn:uuid:` + onvif.UUID() + `</a:MessageID>
    <a:RelatesTo>` + relatesTo + `</a:RelatesTo>
    <a:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:To>
  </s:Header>
  <s:Body>
    <d:ProbeMatches>
      <d:ProbeMatch>
        <a:EndpointReference>
          <a:Address>urn:uuid:` + uuid + `</a:Address>
        </a:EndpointReference>
        <d:Types>dn:NetworkVideoTransmitter</d:Types>
        <d:Scopes>
          onvif://www.onvif.org/type/NetworkVideoTransmitter
          onvif://www.onvif.org/name/go2rtc
          onvif://www.onvif.org/hardware/go2rtc
        </d:Scopes>
        <d:XAddrs>` + xaddrStr + `</d:XAddrs>
        <d:MetadataVersion>1</d:MetadataVersion>
      </d:ProbeMatch>
    </d:ProbeMatches>
  </s:Body>
</s:Envelope>`
}

