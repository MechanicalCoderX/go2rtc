package onvif

import (
	"encoding/base64"
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

// streamOverride holds optional per-stream ONVIF metadata from go2rtc.yaml:
//
//	onvif:
//	  my_camera:
//	    resolution: "1920x1080"
//	    fps: 30
//	    bitrate: 4000
type streamOverride struct {
	Model      string `yaml:"model"`
	Resolution string `yaml:"resolution"`
	FPS        int    `yaml:"fps"`
	Bitrate    int    `yaml:"bitrate"`
}

var streamOverrides map[string]streamOverride

func Init() {
	var cfg struct {
		Mod map[string]streamOverride `yaml:"onvif"`
	}
	app.LoadConfig(&cfg)
	streamOverrides = cfg.Mod

	log = app.GetLogger("onvif")

	streams.HandleFunc("onvif", streamOnvif)

	// ONVIF server on all suburls
	api.HandleFunc("/onvif/", onvifDeviceService)

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
					case core.CodecAAC, core.CodecOpus:
						// ONVIF AudioEncoding only supports G711/G726/AAC; map Opus -> AAC
						// and preserve the actual clock rate so clients SDP-negotiate correctly.
						meta.Audio = "AAC"
						if codec.ClockRate > 0 {
							meta.AudioSampleRate = int(codec.ClockRate)
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

// deviceMeta returns the StreamMeta for the first stream that has any
// device-level config (name, hardware, version). Used for device-wide
// responses such as GetDeviceInformation and GetScopes.
func deviceMeta() *onvif.StreamMeta {
	for _, name := range streams.GetAllNames() {
		if ov, ok := streamOverrides[name]; ok {
			if ov.Model != "" {
				return buildMeta(name)
			}
		}
	}
	return &onvif.StreamMeta{}
}

func onvifDeviceService(w http.ResponseWriter, r *http.Request) {
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

	log.Trace().Msgf("[onvif] server request %s %s:\n%s", r.Method, r.RequestURI, b)

	switch operation {
	case onvif.ServiceGetServiceCapabilities, // important for Hass
		onvif.DeviceGetNetworkInterfaces, // important for Hass
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

	case onvif.DeviceGetCapabilities:
		// important for Hass: Media section
		b = onvif.GetCapabilitiesResponse(r.Host)

	case onvif.DeviceGetServices:
		b = onvif.GetServicesResponse(r.Host)

	case onvif.DeviceGetDeviceInformation:
		// important for Hass: SerialNumber (unique server ID)
		b = onvif.GetDeviceInformationResponse(deviceMeta(), app.Version, r.Host)

	case onvif.DeviceGetScopes:
		b = onvif.GetScopesResponse(deviceMeta())

	case onvif.DeviceSystemReboot:
		b = onvif.StaticResponse(operation)

		time.AfterFunc(time.Second, func() {
			os.Exit(0)
		})

	case onvif.MediaGetVideoSources:
		names := streams.GetAllNames()
		b = onvif.GetVideoSourcesResponse(names, buildMetas(names))

	case onvif.MediaGetProfiles:
		// important for Hass: H264 codec, width, height
		names := streams.GetAllNames()
		b = onvif.GetProfilesResponse(names, buildMetas(names))

	case onvif.MediaGetProfile:
		token := onvif.FindTagValue(b, "ProfileToken")
		b = onvif.GetProfileResponse(token, buildMeta(token))

	case onvif.MediaGetVideoSourceConfigurations:
		// important for Happytime Onvif Client
		names := streams.GetAllNames()
		b = onvif.GetVideoSourceConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetVideoSourceConfiguration:
		token := onvif.FindTagValue(b, "ConfigurationToken")
		b = onvif.GetVideoSourceConfigurationResponse(token, buildMeta(token))

	case onvif.MediaGetVideoEncoderConfigurations:
		names := streams.GetAllNames()
		b = onvif.GetVideoEncoderConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetVideoEncoderConfiguration:
		token := onvif.FindTagValue(b, "ConfigurationToken")
		b = onvif.GetVideoEncoderConfigurationResponse(buildMeta(token))

	case onvif.MediaGetAudioSources:
		names := streams.GetAllNames()
		b = onvif.GetAudioSourcesResponse(names, buildMetas(names))

	case onvif.MediaGetAudioSourceConfigurations:
		names := streams.GetAllNames()
		b = onvif.GetAudioSourceConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetAudioEncoderConfigurations:
		names := streams.GetAllNames()
		b = onvif.GetAudioEncoderConfigurationsResponse(names, buildMetas(names))

	case onvif.MediaGetAudioEncoderConfiguration:
		token := onvif.FindTagValue(b, "ConfigurationToken")
		b = onvif.GetAudioEncoderConfigurationResponse(buildMeta(token))

	case onvif.MediaGetStreamUri:
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host // in case of Host without port
		}

		uri := "rtsp://" + host + ":" + rtsp.Port + "/" + onvif.FindTagValue(b, "ProfileToken")
		b = onvif.GetStreamUriResponse(uri)

	case onvif.MediaGetSnapshotUri:
		uri := "http://" + r.Host + "/api/frame.jpeg?src=" + onvif.FindTagValue(b, "ProfileToken")
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
