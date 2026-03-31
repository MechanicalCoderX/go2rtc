//go:build !linux

package onvif

// ensureVirtualNIC is a no-op on non-Linux platforms. Virtual NIC creation
// for per-stream ONVIF isolation requires Linux (macvlan) and CAP_NET_ADMIN.
// On Windows/macOS, configure ip: per stream only if the IP is already bound
// to a real or virtual network interface on the host.
func ensureVirtualNIC(streamName, targetIP string) (func(), error) {
	log.Warn().Str("stream", streamName).Str("ip", targetIP).
		Msg("[onvif] virtual NIC auto-creation requires Linux; configure the IP manually")
	return func() {}, nil
}

// cleanupOrphanedVirtualNICs is a no-op on non-Linux platforms.
func cleanupOrphanedVirtualNICs() {}
