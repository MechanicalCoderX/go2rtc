package onvif

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/rtsp"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/AlexxIT/go2rtc/pkg/h265"
	"github.com/AlexxIT/go2rtc/pkg/onvif"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// substreamConfig holds optional per-substream ONVIF metadata overrides.
// The sub-stream is enabled whenever the substream: key is present in the YAML,
// regardless of whether any nested fields are set.
// Any unset field falls back to half the parent stream's value.
//
//	substream:                 # enables sub-stream; all fields optional
//	  resolution: "640x360"   # overrides the half-of-main default
//	  fps: 10
//	  bitrate: 1024            # kbps
type substreamConfig struct {
	present    bool   // true when the substream: key appeared in YAML
	Resolution string `yaml:"resolution"`
	FPS        int    `yaml:"fps"`
	Bitrate    int    `yaml:"bitrate"`
}

// UnmarshalYAML marks the sub-stream as present whenever the key exists in the
// YAML document, even if its value is null or an empty mapping.
func (s *substreamConfig) UnmarshalYAML(value *yaml.Node) error {
	s.present = true
	if value.Tag == "!!null" {
		return nil
	}
	type plain substreamConfig
	return value.Decode((*plain)(s))
}

// streamOverride holds optional per-device ONVIF metadata from go2rtc.yaml:
//
//	onvif:
//	  port: 8001                    # shared ONVIF port for all device servers (default: 8001)
//	  devices:
//	    my_camera:
//	      model: "My Camera Model"  # display name shown in NVR/ONVIF clients
//	      resolution: "1920x1080"   # WxH advertised to clients
//	      fps: 30
//	      bitrate: 4096             # kbps
//	      has_audio: true           # applies to both main and sub stream profiles
//	      ip: "192.168.1.100"       # unique IP → go2rtc auto-creates a macvlan NIC (Linux/Docker)
//	      has_audio: true           # nil=auto-detect, true=force-on, false=force-off
//	      audio_codec: "AAC"        # "AAC" or "G711"; overrides live-detected codec name
//	      audio_sample_rate: 22050  # Hz; overrides live-detected sample rate
//	      substream:                # enables sub-stream; go2rtc stream name defaults to "{device}_sub"
//	        resolution: "640x360"   # optional overrides; omit any field to use half the main value
//	        fps: 10
//	        bitrate: 1024
//
// Devices with ip: set are advertised via WS-Discovery as independent ONVIF cameras,
// each with a unique MAC address derived from the stream name.
// Devices without ip: are accessible at /onvif/{stream}/ on the main API port but are
// not advertised via WS-Discovery.
type streamOverride struct {
	Model           string          `yaml:"model"`
	Resolution      string          `yaml:"resolution"`
	FPS             int             `yaml:"fps"`
	Bitrate         int             `yaml:"bitrate"`
	HasAudio        *bool           `yaml:"has_audio"`
	AudioCodec      string          `yaml:"audio_codec"`       // "AAC" or "G711"; overrides live-detected codec
	AudioSampleRate int             `yaml:"audio_sample_rate"` // Hz; overrides live-detected sample rate
	IP              string          `yaml:"ip"`
	Substream       substreamConfig `yaml:"substream"`
}

var streamOverrides map[string]streamOverride

// subStreamNames maps main device stream name → resolved sub-stream name.
// Built at startup from devices that have a substream: key (defaulting to "{main}_sub").
var subStreamNames map[string]string

// subParentNames maps resolved sub-stream name → main device stream name.
// Used by buildMeta to source "half of main" fallback values.
var subParentNames map[string]string

var onvifPort int

func Init() {
	var cfg struct {
		Mod struct {
			Port    int                       `yaml:"port"`
			Devices map[string]streamOverride `yaml:"devices"`
		} `yaml:"onvif"`
	}
	cfg.Mod.Port = 8001 // default
	app.LoadConfig(&cfg)
	streamOverrides = cfg.Mod.Devices
	onvifPort = cfg.Mod.Port

	// Build sub-stream lookup tables.
	subStreamNames = make(map[string]string)
	subParentNames = make(map[string]string)
	for mainName, ov := range streamOverrides {
		if !ov.Substream.present {
			continue
		}
		subName := mainName + "_sub"
		subStreamNames[mainName] = subName
		subParentNames[subName] = mainName
	}

	log = app.GetLogger("onvif")

	streams.HandleFunc("onvif", streamOnvif)

	// Per-stream ONVIF endpoints on the main API port (not advertised via WS-Discovery).
	// Useful for direct-URL clients such as Home Assistant.
	api.HandleFunc("/onvif/{stream}/", onvifDeviceService)

	// ONVIF client autodiscovery
	api.HandleFunc("api/onvif", apiOnvif)

	// For each stream with ip: set, create a virtual NIC (Linux/Docker) and start a
	// dedicated ONVIF HTTP server on that IP. Each unique IP → unique ARP MAC →
	// Protect treats each stream as an independent camera.
	cleanupOrphanedVirtualNICs()
	for name, ov := range streamOverrides {
		if ov.IP == "" {
			continue
		}
		cleanup, err := ensureVirtualNIC(name, ov.IP)
		if err != nil {
			log.Warn().Err(err).Str("stream", name).Msg("[onvif] failed to create virtual NIC")
			continue
		}
		virtualNICCleanups = append(virtualNICCleanups, cleanup)
		go listenStream(name, ov.IP, onvifPort)
	}

	// WS-Discovery server: advertise only streams with ip: as independent ONVIF devices
	go StartDiscovery()
}

var log zerolog.Logger
var virtualNICCleanups []func()

// listenStream starts a dedicated HTTP server on ip:port that serves all ONVIF
// requests as belonging to the named stream. Each stream gets a unique IP (via a
// virtual macvlan NIC), giving it a unique ARP MAC so NVRs like UniFi Protect
// treat it as an independent camera.
func listenStream(name, ip string, port int) {
	addr := fmt.Sprintf("%s:%d", ip, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Warn().Err(err).Str("stream", name).Msgf("[onvif] failed to bind %s", addr)
		return
	}
	log.Info().Str("stream", name).Msgf("[onvif] listening on %s", addr)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveStreamDirect(w, r, name)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if err = server.Serve(ln); err != nil {
		log.Error().Err(err).Str("stream", name).Msg("[onvif] dedicated server error")
	}
}

// serveStreamDirect handles an ONVIF request for a specific stream without
// needing the stream name to be embedded in the URL path.
func serveStreamDirect(w http.ResponseWriter, r *http.Request, stream string) {
	if streams.Get(stream) == nil {
		http.Error(w, "stream not found", http.StatusNotFound)
		return
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	operation := onvif.GetRequestAction(b)
	if operation == "" {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}

	log.Trace().Msgf("[onvif] server request stream=%s %s %s:\n%s", stream, r.Method, r.RequestURI, b)

	names := streamsForDevice(stream)
	meta := buildMeta(stream)

	var out []byte
	switch operation {
	case onvif.ServiceGetServiceCapabilities,
		onvif.DeviceGetSystemDateAndTime,
		onvif.DeviceSetSystemDateAndTime,
		onvif.DeviceGetDiscoveryMode,
		onvif.DeviceGetDNS,
		onvif.DeviceGetHostname,
		onvif.DeviceGetNetworkDefaultGateway,
		onvif.DeviceGetNetworkProtocols,
		onvif.DeviceGetNTP,
		onvif.MediaGetVideoEncoderConfigurationOptions:
		out = onvif.StaticResponse(operation)

	case onvif.DeviceGetNetworkInterfaces:
		out = onvif.GetNetworkInterfacesResponse(onvif.StreamMAC(stream))

	case onvif.DeviceGetCapabilities:
		out = onvif.GetCapabilitiesResponse(r.Host, stream)

	case onvif.DeviceGetServices:
		out = onvif.GetServicesResponse(r.Host, stream)

	case onvif.DeviceGetDeviceInformation:
		out = onvif.GetDeviceInformationResponse(meta, app.Version, onvif.StreamSerial(stream))

	case onvif.DeviceGetScopes:
		out = onvif.GetScopesResponse(meta)

	case onvif.DeviceSystemReboot:
		out = onvif.StaticResponse(operation)
		time.AfterFunc(time.Second, func() { os.Exit(0) })

	case onvif.MediaGetVideoSources:
		out = onvif.GetVideoSourcesResponse(names, buildMetas(names))
	case onvif.MediaGetProfiles:
		out = onvif.GetProfilesResponse(names, buildMetas(names))
	case onvif.MediaGetProfile:
		target := profileStream(onvif.FindTagValue(b, "ProfileToken"), stream)
		out = onvif.GetProfileResponse(target, buildMeta(target))
	case onvif.MediaGetVideoSourceConfigurations:
		out = onvif.GetVideoSourceConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetVideoSourceConfiguration:
		out = onvif.GetVideoSourceConfigurationResponse(stream, meta)
	case onvif.MediaGetVideoEncoderConfigurations:
		out = onvif.GetVideoEncoderConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetVideoEncoderConfiguration:
		out = onvif.GetVideoEncoderConfigurationResponse(meta)
	case onvif.MediaGetAudioSources:
		out = onvif.GetAudioSourcesResponse(names, buildMetas(names))
	case onvif.MediaGetAudioSourceConfigurations:
		out = onvif.GetAudioSourceConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetAudioEncoderConfigurations:
		out = onvif.GetAudioEncoderConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetAudioEncoderConfiguration:
		out = onvif.GetAudioEncoderConfigurationResponse(meta)

	case onvif.MediaGetStreamUri:
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		target := profileStream(onvif.FindTagValue(b, "ProfileToken"), stream)
		uri := "rtsp://" + host + ":" + rtsp.Port + "/" + target
		out = onvif.GetStreamUriResponse(uri)

	case onvif.MediaGetSnapshotUri:
		// r.Host is the dedicated stream IP:onvifPort; snapshot is on the main API port.
		// Snapshots are only available for the main stream.
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		uri := fmt.Sprintf("http://%s:%d/api/frame.jpeg?src=%s", host, api.Port, stream)
		out = onvif.GetSnapshotUriResponse(uri)

	default:
		http.Error(w, "unsupported operation", http.StatusBadRequest)
		log.Warn().Msgf("[onvif] unsupported operation: %s", operation)
		return
	}

	log.Trace().Msgf("[onvif] server response:\n%s", out)
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	if _, err = w.Write(out); err != nil {
		log.Error().Err(err).Caller().Send()
	}
}

func streamOnvif(rawURL string) (core.Producer, error) {
	client, err := onvif.NewClient(rawURL)
	if err != nil {
		return nil, err
	}

	uri, err := client.GetURI()
	if err != nil {
		return nil, err
	}

	// Append hash-based arguments to the retrieved URI
	if i := strings.IndexByte(rawURL, '#'); i > 0 {
		uri += rawURL[i:]
	}

	log.Debug().Msgf("[onvif] new uri=%s", uri)

	if err = streams.Validate(uri); err != nil {
		return nil, err
	}

	return streams.GetProducer(uri)
}

// buildMeta constructs a StreamMeta for the named stream by combining live
// codec information (when available) with any YAML config overrides.
func buildMeta(name string) *onvif.StreamMeta {
	meta := &onvif.StreamMeta{}

	// Populate from live stream medias when the producer is connected.
	if stream := streams.Get(name); stream != nil {
		for _, media := range stream.GetMedias() {
			switch media.Kind {
			case core.KindVideo:
				if len(media.Codecs) == 0 {
					continue
				}
				codec := media.Codecs[0]
				switch codec.Name {
				case core.CodecH264:
					meta.Video = "H264"
					if w, h := resolutionFromH264(codec.FmtpLine); w > 0 {
						meta.Width, meta.Height = w, h
					}
				case core.CodecH265:
					meta.Video = "H265"
					if w, h := resolutionFromH265(codec.FmtpLine); w > 0 {
						meta.Width, meta.Height = w, h
					}
				}
			case core.KindAudio:
				meta.HasAudio = true
				if len(media.Codecs) > 0 && meta.Audio == "" {
					codec := media.Codecs[0]
					switch codec.Name {
					case core.CodecPCMA, core.CodecPCMU:
						meta.Audio = "G711"
						meta.AudioSampleRate = 8000
						meta.AudioChannels = 1
					case core.CodecAAC, core.CodecOpus:
						// ONVIF AudioEncoding enum only defines G711/G726/AAC; Opus must
						// map to AAC. Sample rate and channel count are still reported
						// accurately from the live codec.
						meta.Audio = "AAC"
						if codec.ClockRate > 0 {
							meta.AudioSampleRate = int(codec.ClockRate)
						}
						if codec.Channels > 0 {
							meta.AudioChannels = int(codec.Channels)
						}
					}
				}
			}
		}
	}

	// Apply YAML config overrides (take precedence over live values).
	if ov, ok := streamOverrides[name]; ok {
		if ov.Resolution != "" {
			if w, h := parseResolution(ov.Resolution); w > 0 {
				meta.Width, meta.Height = w, h
			}
		}
		if ov.FPS > 0 {
			meta.FPS = ov.FPS
		}
		if ov.Bitrate > 0 {
			meta.Bitrate = ov.Bitrate
		}
		if ov.Model != "" {
			meta.Model = ov.Model
		}
		// has_audio: true/false overrides live auto-detection.
		// Omitting has_audio entirely leaves the live-detected value in place.
		if ov.HasAudio != nil {
			meta.HasAudio = *ov.HasAudio
		}
		if ov.AudioCodec != "" {
			meta.Audio = ov.AudioCodec
		}
		if ov.AudioSampleRate > 0 {
			meta.AudioSampleRate = ov.AudioSampleRate
		}
	} else if mainName, ok := subParentNames[name]; ok {
		// Sub-stream inherits has_audio override from the parent device.
		if mainOv, exists := streamOverrides[mainName]; exists && mainOv.HasAudio != nil {
			meta.HasAudio = *mainOv.HasAudio
		}
	}

	// For sub-streams: apply explicit substream: overrides from the parent device
	// config first, then fall back to halving the main stream's value for any
	// field still at zero. If the main stream also has no value, half the
	// StreamMeta hard-coded fallback is used (960×540, 15 fps, 4096 kbps).
	if mainName, ok := subParentNames[name]; ok {
		if mainOv, exists := streamOverrides[mainName]; exists {
			sub := mainOv.Substream
			if sub.Resolution != "" {
				if w, h := parseResolution(sub.Resolution); w > 0 {
					meta.Width, meta.Height = w, h
				}
			}
			if sub.FPS > 0 {
				meta.FPS = sub.FPS
			}
			if sub.Bitrate > 0 {
				meta.Bitrate = sub.Bitrate
			}
		}
		main := buildMeta(mainName)
		if meta.Width == 0 {
			meta.Width = halfOf(main.Width, 1920)
		}
		if meta.Height == 0 {
			meta.Height = halfOf(main.Height, 1080)
		}
		if meta.FPS == 0 {
			meta.FPS = max(1, halfOf(main.FPS, 30))
		}
		if meta.Bitrate == 0 {
			meta.Bitrate = halfOf(main.Bitrate, 8192)
		}
		if meta.Video == "" {
			meta.Video = main.Video
		}
	}

	return meta
}

// halfOf returns v/2 when v > 0, otherwise returns fallback/2.
func halfOf(v, fallback int) int {
	if v > 0 {
		return v / 2
	}
	return fallback / 2
}

// buildMetas returns a name→meta map for a slice of stream names.
func buildMetas(names []string) map[string]*onvif.StreamMeta {
	metas := make(map[string]*onvif.StreamMeta, len(names))
	for _, name := range names {
		metas[name] = buildMeta(name)
	}
	return metas
}

// streamsForDevice returns the ONVIF profile names for a device: always the
// main stream, plus the sub stream name if one is configured.
func streamsForDevice(mainName string) []string {
	if subName, ok := subStreamNames[mainName]; ok {
		return []string{mainName, subName}
	}
	return []string{mainName}
}

// profileStream maps an ONVIF ProfileToken to the go2rtc stream name that
// should be used for GetStreamUri / GetProfile. Falls back to mainName if the
// token is unrecognised.
func profileStream(token, mainName string) string {
	if token == mainName {
		return mainName
	}
	if _, ok := subParentNames[token]; ok {
		return token
	}
	return mainName
}

// resolutionFromH264 extracts width/height from the sprop-parameter-sets in an
// H264 fmtp line by decoding the SPS NALU. Returns (0,0) on failure.
func resolutionFromH264(fmtpLine string) (int, int) {
	ps := core.Between(fmtpLine, "sprop-parameter-sets=", ",")
	if ps == "" {
		return 0, 0
	}
	spsBytes, err := base64.StdEncoding.DecodeString(ps)
	if err != nil || len(spsBytes) < 4 {
		return 0, 0
	}
	sps := h264.DecodeSPS(spsBytes)
	if sps == nil {
		return 0, 0
	}
	return int(sps.Width()), int(sps.Height())
}

// resolutionFromH265 extracts width/height from the sprop-sps in an H265 fmtp
// line. Returns (0,0) on failure.
func resolutionFromH265(fmtpLine string) (int, int) {
	_, spsBytes, _ := h265.GetParameterSet(fmtpLine)
	if len(spsBytes) < 2 {
		return 0, 0
	}
	sps := h265.DecodeSPS(spsBytes)
	if sps == nil {
		return 0, 0
	}
	return int(sps.Width()), int(sps.Height())
}

// parseResolution parses a "WxH" string (e.g. "1920x1080").
// Returns (0,0) if the string is not in the expected format.
func parseResolution(s string) (int, int) {
	i := strings.IndexByte(s, 'x')
	if i <= 0 {
		return 0, 0
	}
	w, err := strconv.Atoi(s[:i])
	if err != nil || w <= 0 {
		return 0, 0
	}
	h, err := strconv.Atoi(s[i+1:])
	if err != nil || h <= 0 {
		return 0, 0
	}
	return w, h
}

func onvifDeviceService(w http.ResponseWriter, r *http.Request) {
	stream := r.PathValue("stream")
	if streams.Get(stream) == nil {
		http.Error(w, "stream not found", http.StatusNotFound)
		return
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	operation := onvif.GetRequestAction(b)
	if operation == "" {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}

	log.Trace().Msgf("[onvif] server request stream=%s %s %s:\n%s", stream, r.Method, r.RequestURI, b)

	names := streamsForDevice(stream)
	meta := buildMeta(stream)

	switch operation {
	case onvif.ServiceGetServiceCapabilities, // important for Hass
		onvif.DeviceGetSystemDateAndTime, // important for Hass
		onvif.DeviceSetSystemDateAndTime, // return just OK
		onvif.DeviceGetDiscoveryMode,
		onvif.DeviceGetDNS,
		onvif.DeviceGetHostname,
		onvif.DeviceGetNetworkDefaultGateway,
		onvif.DeviceGetNetworkProtocols,
		onvif.DeviceGetNTP,
		onvif.MediaGetVideoEncoderConfigurationOptions:
		b = onvif.StaticResponse(operation)

	case onvif.DeviceGetNetworkInterfaces:
		// important for Hass; unique MAC per stream so NVRs treat each stream as a distinct device
		b = onvif.GetNetworkInterfacesResponse(onvif.StreamMAC(stream))

	case onvif.DeviceGetCapabilities:
		// important for Hass: Media section
		b = onvif.GetCapabilitiesResponse(r.Host, stream)

	case onvif.DeviceGetServices:
		b = onvif.GetServicesResponse(r.Host, stream)

	case onvif.DeviceGetDeviceInformation:
		// important for Hass: SerialNumber (unique per stream)
		b = onvif.GetDeviceInformationResponse(meta, app.Version, onvif.StreamSerial(stream))

	case onvif.DeviceGetScopes:
		b = onvif.GetScopesResponse(meta)

	case onvif.DeviceSystemReboot:
		b = onvif.StaticResponse(operation)

		time.AfterFunc(time.Second, func() {
			os.Exit(0)
		})

	case onvif.MediaGetVideoSources:
		b = onvif.GetVideoSourcesResponse(names, buildMetas(names))

	case onvif.MediaGetProfiles:
		// important for Hass: H264 codec, width, height
		b = onvif.GetProfilesResponse(names, buildMetas(names))

	case onvif.MediaGetProfile:
		target := profileStream(onvif.FindTagValue(b, "ProfileToken"), stream)
		b = onvif.GetProfileResponse(target, buildMeta(target))

	case onvif.MediaGetVideoSourceConfigurations:
		// important for Happytime Onvif Client
		b = onvif.GetVideoSourceConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetVideoSourceConfiguration:
		b = onvif.GetVideoSourceConfigurationResponse(stream, meta)

	case onvif.MediaGetVideoEncoderConfigurations:
		b = onvif.GetVideoEncoderConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetVideoEncoderConfiguration:
		b = onvif.GetVideoEncoderConfigurationResponse(meta)

	case onvif.MediaGetAudioSources:
		b = onvif.GetAudioSourcesResponse(names, buildMetas(names))

	case onvif.MediaGetAudioSourceConfigurations:
		b = onvif.GetAudioSourceConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetAudioEncoderConfigurations:
		b = onvif.GetAudioEncoderConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetAudioEncoderConfiguration:
		b = onvif.GetAudioEncoderConfigurationResponse(meta)

	case onvif.MediaGetStreamUri:
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host // in case of Host without port
		}
		target := profileStream(onvif.FindTagValue(b, "ProfileToken"), stream)
		b = onvif.GetStreamUriResponse("rtsp://" + host + ":" + rtsp.Port + "/" + target)

	case onvif.MediaGetSnapshotUri:
		// r.Host is the main API host:port, so snapshot URL is already correct.
		// Snapshots are only available for the main stream.
		uri := "http://" + r.Host + "/api/frame.jpeg?src=" + stream
		b = onvif.GetSnapshotUriResponse(uri)

	default:
		http.Error(w, "unsupported operation", http.StatusBadRequest)
		log.Warn().Msgf("[onvif] unsupported operation: %s", operation)
		log.Debug().Msgf("[onvif] unsupported request:\n%s", b)
		return
	}

	log.Trace().Msgf("[onvif] server response:\n%s", b)

	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	if _, err = w.Write(b); err != nil {
		log.Error().Err(err).Caller().Send()
	}
}

func apiOnvif(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("src")

	var items []*api.Source

	if src == "" {
		devices, err := onvif.DiscoveryStreamingDevices()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, device := range devices {
			u, err := url.Parse(device.URL)
			if err != nil {
				log.Warn().Str("url", device.URL).Msg("[onvif] broken")
				continue
			}

			if u.Scheme != "http" {
				log.Warn().Str("url", device.URL).Msg("[onvif] unsupported")
				continue
			}

			u.Scheme = "onvif"
			u.User = url.UserPassword("user", "pass")

			if u.Path == onvif.PathDevice {
				u.Path = ""
			}

			items = append(items, &api.Source{
				Name: u.Host,
				URL:  u.String(),
				Info: device.Name + " " + device.Hardware,
			})
		}
	} else {
		client, err := onvif.NewClient(src)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if l := log.Trace(); l.Enabled() {
			b, _ := client.MediaRequest(onvif.MediaGetProfiles)
			l.Msgf("[onvif] src=%s profiles:\n%s", src, b)
		}

		name, err := client.GetName()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tokens, err := client.GetProfilesTokens()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for i, token := range tokens {
			items = append(items, &api.Source{
				Name: name + " stream" + strconv.Itoa(i),
				URL:  src + "?subtype=" + token,
			})
		}

		if len(tokens) > 0 && client.HasSnapshots() {
			items = append(items, &api.Source{
				Name: name + " snapshot",
				URL:  src + "?subtype=" + tokens[0] + "&snapshot",
			})
		}
	}

	api.ResponseSources(w, items)
}
