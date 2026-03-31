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
)

// streamOverride holds optional per-device ONVIF metadata from go2rtc.yaml:
//
//	onvif:
//	  devices:
//	    my_camera:
//	      model: "My Camera Model"  # display name shown in NVR/ONVIF clients
//	      resolution: "1920x1080"   # WxH advertised to clients
//	      fps: 30
//	      bitrate: 4096             # kbps
type streamOverride struct {
	Model      string `yaml:"model"`
	Resolution string `yaml:"resolution"`
	FPS        int    `yaml:"fps"`
	Bitrate    int    `yaml:"bitrate"`
}

var streamOverrides map[string]streamOverride

func Init() {
	var cfg struct {
		Mod struct {
			Devices map[string]streamOverride `yaml:"devices"`
		} `yaml:"onvif"`
	}
	app.LoadConfig(&cfg)
	streamOverrides = cfg.Mod.Devices

	log = app.GetLogger("onvif")

	streams.HandleFunc("onvif", streamOnvif)

	// Per-stream ONVIF endpoints on the main API port.
	// Useful for direct-URL clients such as Home Assistant.
	api.HandleFunc("/onvif/{stream}/", onvifDeviceService)

	// ONVIF client autodiscovery
	api.HandleFunc("api/onvif", apiOnvif)
}

var log zerolog.Logger

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
	}

	return meta
}

// buildMetas returns a name→meta map for a slice of stream names.
func buildMetas(names []string) map[string]*onvif.StreamMeta {
	metas := make(map[string]*onvif.StreamMeta, len(names))
	for _, name := range names {
		metas[name] = buildMeta(name)
	}
	return metas
}

// streamsForDevice returns the ONVIF profile names for a device.
func streamsForDevice(mainName string) []string {
	return []string{mainName}
}

// buildOnvifResponse constructs the SOAP response for a single ONVIF operation.
// snapshotURI is pre-computed by the caller. Returns nil for unrecognised operations.
func buildOnvifResponse(operation string, r *http.Request, stream, snapshotURI string) []byte {
	names := streamsForDevice(stream)
	meta := buildMeta(stream)

	switch operation {
	case onvif.ServiceGetServiceCapabilities,
		onvif.DeviceGetSystemDateAndTime,
		onvif.DeviceSetSystemDateAndTime,
		onvif.DeviceGetDiscoveryMode,
		onvif.DeviceGetDNS,
		onvif.DeviceGetHostname,
		onvif.DeviceGetNetworkDefaultGateway,
		onvif.DeviceGetNetworkInterfaces,
		onvif.DeviceGetNetworkProtocols,
		onvif.DeviceGetNTP,
		onvif.MediaGetVideoEncoderConfigurationOptions:
		return onvif.StaticResponse(operation)

	case onvif.DeviceGetCapabilities:
		return onvif.GetCapabilitiesResponse(r.Host, stream)

	case onvif.DeviceGetServices:
		return onvif.GetServicesResponse(r.Host, stream)

	case onvif.DeviceGetDeviceInformation:
		return onvif.GetDeviceInformationResponse(meta, app.Version, onvif.StreamSerial(stream))

	case onvif.DeviceGetScopes:
		return onvif.GetScopesResponse(meta)

	case onvif.DeviceSystemReboot:
		time.AfterFunc(time.Second, func() { os.Exit(0) })
		return onvif.StaticResponse(operation)

	case onvif.MediaGetVideoSources:
		return onvif.GetVideoSourcesResponse(names, buildMetas(names))
	case onvif.MediaGetProfiles:
		return onvif.GetProfilesResponse(names, buildMetas(names))
	case onvif.MediaGetProfile:
		return onvif.GetProfileResponse(stream, meta)
	case onvif.MediaGetVideoSourceConfigurations:
		return onvif.GetVideoSourceConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetVideoSourceConfiguration:
		return onvif.GetVideoSourceConfigurationResponse(stream, meta)
	case onvif.MediaGetVideoEncoderConfigurations:
		return onvif.GetVideoEncoderConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetVideoEncoderConfiguration:
		return onvif.GetVideoEncoderConfigurationResponse(meta)
	case onvif.MediaGetAudioSources:
		return onvif.GetAudioSourcesResponse(names, buildMetas(names))
	case onvif.MediaGetAudioSourceConfigurations:
		return onvif.GetAudioSourceConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetAudioEncoderConfigurations:
		return onvif.GetAudioEncoderConfigurationsResponse(names, buildMetas(names))
	case onvif.MediaGetAudioEncoderConfiguration:
		return onvif.GetAudioEncoderConfigurationResponse(meta)

	case onvif.MediaGetStreamUri:
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		return onvif.GetStreamUriResponse(fmt.Sprintf("rtsp://%s:%s/%s", host, rtsp.Port, stream))

	case onvif.MediaGetSnapshotUri:
		return onvif.GetSnapshotUriResponse(snapshotURI)
	}

	return nil
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

	snapshotURI := "http://" + r.Host + "/api/frame.jpeg?src=" + stream

	out := buildOnvifResponse(operation, r, stream, snapshotURI)
	if out == nil {
		http.Error(w, "unsupported operation", http.StatusBadRequest)
		log.Warn().Msgf("[onvif] unsupported operation: %s", operation)
		log.Debug().Msgf("[onvif] unsupported request:\n%s", b)
		return
	}

	log.Trace().Msgf("[onvif] server response:\n%s", out)
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	if _, err = w.Write(out); err != nil {
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
