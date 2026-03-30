package onvif

import (
	"bytes"
	"regexp"
	"time"
)

const ServiceGetServiceCapabilities = "GetServiceCapabilities"

const (
	DeviceGetCapabilities          = "GetCapabilities"
	DeviceGetDeviceInformation     = "GetDeviceInformation"
	DeviceGetDiscoveryMode         = "GetDiscoveryMode"
	DeviceGetDNS                   = "GetDNS"
	DeviceGetHostname              = "GetHostname"
	DeviceGetNetworkDefaultGateway = "GetNetworkDefaultGateway"
	DeviceGetNetworkInterfaces     = "GetNetworkInterfaces"
	DeviceGetNetworkProtocols      = "GetNetworkProtocols"
	DeviceGetNTP                   = "GetNTP"
	DeviceGetScopes                = "GetScopes"
	DeviceGetServices              = "GetServices"
	DeviceGetSystemDateAndTime     = "GetSystemDateAndTime"
	DeviceSetSystemDateAndTime     = "SetSystemDateAndTime"
	DeviceSystemReboot             = "SystemReboot"
)

const (
	MediaGetAudioEncoderConfiguration       = "GetAudioEncoderConfiguration"
	MediaGetAudioEncoderConfigurations       = "GetAudioEncoderConfigurations"
	MediaGetAudioSources                     = "GetAudioSources"
	MediaGetAudioSourceConfigurations        = "GetAudioSourceConfigurations"
	MediaGetProfile                          = "GetProfile"
	MediaGetProfiles                         = "GetProfiles"
	MediaGetSnapshotUri                      = "GetSnapshotUri"
	MediaGetStreamUri                        = "GetStreamUri"
	MediaGetVideoEncoderConfiguration        = "GetVideoEncoderConfiguration"
	MediaGetVideoEncoderConfigurations       = "GetVideoEncoderConfigurations"
	MediaGetVideoEncoderConfigurationOptions = "GetVideoEncoderConfigurationOptions"
	MediaGetVideoSources                     = "GetVideoSources"
	MediaGetVideoSourceConfiguration         = "GetVideoSourceConfiguration"
	MediaGetVideoSourceConfigurations        = "GetVideoSourceConfigurations"
)

// StreamMeta holds per-stream metadata used to populate ONVIF responses.
// Zero/empty values fall back to sensible defaults.
type StreamMeta struct {
	Width           int    // video width in pixels; 0 → 1920
	Height          int    // video height in pixels; 0 → 1080
	FPS             int    // frames per second; 0 → 30
	Bitrate         int    // video bitrate limit in kbps; 0 → 8192
	Video           string // ONVIF encoding name ("H264" or "H265"); "" → "H264"
	HasAudio        bool   // whether the stream carries an audio track
	Audio           string // ONVIF audio encoding ("G711" or "AAC"); "" → "AAC"
	AudioSampleRate int    // audio sample rate in Hz; 0 → 22050 (AAC) or 8000 (G711)
	AudioChannels   int    // audio channel count; 0 → 1 (mono)
	Name            string // friendly display name shown in ONVIF clients; empty → stream key
	Model           string // device model name (e.g. camera model); shown in Protect/ONVIF clients
	Version         string // device version string; shown as "device version" in Protect
}

// nil-safe accessor methods — video

func (m *StreamMeta) w() int {
	if m != nil && m.Width > 0 {
		return m.Width
	}
	return 1920
}

func (m *StreamMeta) h() int {
	if m != nil && m.Height > 0 {
		return m.Height
	}
	return 1080
}

func (m *StreamMeta) fps() int {
	if m != nil && m.FPS > 0 {
		return m.FPS
	}
	return 30
}

func (m *StreamMeta) bitrate() int {
	if m != nil && m.Bitrate > 0 {
		return m.Bitrate
	}
	return 8192
}

func (m *StreamMeta) video() string {
	if m != nil && m.Video != "" {
		return m.Video
	}
	return "H264"
}

// nil-safe accessor methods — audio

func (m *StreamMeta) modelName(fallback string) string {
	if m != nil && m.Model != "" {
		return m.Model
	}
	return fallback
}

func (m *StreamMeta) audio() string {
	if m != nil && m.Audio != "" {
		return m.Audio
	}
	return "AAC"
}

func (m *StreamMeta) audioSampleRate() int {
	if m != nil {
		if m.AudioSampleRate > 0 {
			return m.AudioSampleRate
		}
		if m.Audio == "G711" {
			return 8000
		}
	}
	return 22050
}

func (m *StreamMeta) audioChannels() int {
	if m != nil && m.AudioChannels > 0 {
		return m.AudioChannels
	}
	return 1
}

func GetRequestAction(b []byte) string {
	// <soap-env:Body><ns0:GetCapabilities xmlns:ns0="http://www.onvif.org/ver10/device/wsdl">
	// <v:Body><GetSystemDateAndTime xmlns="http://www.onvif.org/ver10/device/wsdl" /></v:Body>
	re := regexp.MustCompile(`Body[^<]+<([^ />]+)`)
	m := re.FindSubmatch(b)
	if len(m) != 2 {
		return ""
	}
	if i := bytes.IndexByte(m[1], ':'); i > 0 {
		return string(m[1][i+1:])
	}
	return string(m[1])
}

func GetCapabilitiesResponse(host, stream string) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetCapabilitiesResponse>
	<tds:Capabilities>
		<tt:Device>
			<tt:XAddr>http://%s/onvif/%s/device_service</tt:XAddr>
		</tt:Device>
		<tt:Media>
			<tt:XAddr>http://%s/onvif/%s/media_service</tt:XAddr>
			<tt:StreamingCapabilities>
				<tt:RTPMulticast>false</tt:RTPMulticast>
				<tt:RTP_TCP>false</tt:RTP_TCP>
				<tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP>
			</tt:StreamingCapabilities>
		</tt:Media>
	</tds:Capabilities>
</tds:GetCapabilitiesResponse>`, host, stream, host, stream)
	return e.Bytes()
}

func GetServicesResponse(host, stream string) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetServicesResponse>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace>
		<tds:XAddr>http://%s/onvif/%s/device_service</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>5</tt:Minor></tds:Version>
	</tds:Service>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace>
		<tds:XAddr>http://%s/onvif/%s/media_service</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>5</tt:Minor></tds:Version>
	</tds:Service>
</tds:GetServicesResponse>`, host, stream, host, stream)
	return e.Bytes()
}

func GetSystemDateAndTimeResponse() []byte {
	loc := time.Now()
	utc := loc.UTC()

	e := NewEnvelope()
	e.Appendf(`<tds:GetSystemDateAndTimeResponse>
	<tds:SystemDateAndTime>
		<tt:DateTimeType>NTP</tt:DateTimeType>
		<tt:DaylightSavings>true</tt:DaylightSavings>
		<tt:TimeZone>
			<tt:TZ>%s</tt:TZ>
		</tt:TimeZone>
		<tt:UTCDateTime>
			<tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time>
			<tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date>
		</tt:UTCDateTime>
		<tt:LocalDateTime>
			<tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time>
			<tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date>
		</tt:LocalDateTime>
	</tds:SystemDateAndTime>
</tds:GetSystemDateAndTimeResponse>`,
		GetPosixTZ(loc),
		utc.Hour(), utc.Minute(), utc.Second(), utc.Year(), utc.Month(), utc.Day(),
		loc.Hour(), loc.Minute(), loc.Second(), loc.Year(), loc.Month(), loc.Day(),
	)
	return e.Bytes()
}

func GetNetworkInterfacesResponse(mac string) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetNetworkInterfacesResponse>
	<tds:NetworkInterfaces token="eth0">
		<tt:Enabled>true</tt:Enabled>
		<tt:Info>
			<tt:Name>eth0</tt:Name>
			<tt:HwAddress>%s</tt:HwAddress>
		</tt:Info>
	</tds:NetworkInterfaces>
</tds:GetNetworkInterfacesResponse>`, mac)
	return e.Bytes()
}

func GetDeviceInformationResponse(meta *StreamMeta, firmware, serial string) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetDeviceInformationResponse>
	<tds:Manufacturer></tds:Manufacturer>
	<tds:Model>%s</tds:Model>
	<tds:FirmwareVersion>%s</tds:FirmwareVersion>
	<tds:SerialNumber>%s</tds:SerialNumber>
	<tds:HardwareId>%s</tds:HardwareId>
</tds:GetDeviceInformationResponse>`, meta.modelName("go2rtc"), firmware, serial, "1.00")
	return e.Bytes()
}

func GetProfilesResponse(names []string, metas map[string]*StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetProfilesResponse>`)
	for _, name := range names {
		appendProfile(e, "Profiles", name, metas[name])
	}
	e.Append(`</trt:GetProfilesResponse>`)
	return e.Bytes()
}

func GetProfileResponse(name string, meta *StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetProfileResponse>`)
	appendProfile(e, "Profile", name, meta)
	e.Append(`</trt:GetProfileResponse>`)
	return e.Bytes()
}

func appendProfile(e *Envelope, tag, name string, meta *StreamMeta) {
	// go2rtc name = ONVIF Profile Name = ONVIF Profile token
	e.Appendf(`<trt:%s token="%s" fixed="true">`, tag, name)
	e.Appendf(`<tt:Name>%s</tt:Name>`, meta.modelName(name))
	appendVideoSourceConfiguration(e, "VideoSourceConfiguration", name, meta)
	if meta != nil && meta.HasAudio {
		appendAudioSourceConfiguration(e, "AudioSourceConfiguration", name)
	}
	appendVideoEncoderConfiguration(e, "VideoEncoderConfiguration", meta)
	if meta != nil && meta.HasAudio {
		appendAudioEncoderConfiguration(e, "AudioEncoderConfiguration", meta)
	}
	e.Appendf(`</trt:%s>`, tag)
}

func GetVideoSourcesResponse(names []string, metas map[string]*StreamMeta) []byte {
	// go2rtc name = ONVIF VideoSource token
	e := NewEnvelope()
	e.Append(`<trt:GetVideoSourcesResponse>`)
	for _, name := range names {
		m := metas[name]
		e.Appendf(`<trt:VideoSources token="%s">
	<tt:Framerate>%d.000000</tt:Framerate>
	<tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution>
</trt:VideoSources>`, name, m.fps(), m.w(), m.h())
	}
	e.Append(`</trt:GetVideoSourcesResponse>`)
	return e.Bytes()
}

func GetVideoSourceConfigurationsResponse(names []string, metas map[string]*StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetVideoSourceConfigurationsResponse>`)
	for _, name := range names {
		appendVideoSourceConfiguration(e, "Configurations", name, metas[name])
	}
	e.Append(`</trt:GetVideoSourceConfigurationsResponse>`)
	return e.Bytes()
}

func GetVideoSourceConfigurationResponse(name string, meta *StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetVideoSourceConfigurationResponse>`)
	appendVideoSourceConfiguration(e, "Configuration", name, meta)
	e.Append(`</trt:GetVideoSourceConfigurationResponse>`)
	return e.Bytes()
}

func appendVideoSourceConfiguration(e *Envelope, tag, name string, meta *StreamMeta) {
	// go2rtc name = ONVIF VideoSourceConfiguration token
	e.Appendf(`<tt:%s token="%s" fixed="true">
	<tt:Name>VSC</tt:Name>
	<tt:SourceToken>%s</tt:SourceToken>
	<tt:Bounds x="0" y="0" width="%d" height="%d"></tt:Bounds>
</tt:%s>`, tag, name, name, meta.w(), meta.h(), tag)
}

func GetVideoEncoderConfigurationsResponse(names []string, metas map[string]*StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetVideoEncoderConfigurationsResponse>`)
	for _, name := range names {
		appendVideoEncoderConfiguration(e, "VideoEncoderConfigurations", metas[name])
	}
	e.Append(`</trt:GetVideoEncoderConfigurationsResponse>`)
	return e.Bytes()
}

func GetVideoEncoderConfigurationResponse(meta *StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetVideoEncoderConfigurationResponse>`)
	appendVideoEncoderConfiguration(e, "VideoEncoderConfiguration", meta)
	e.Append(`</trt:GetVideoEncoderConfigurationResponse>`)
	return e.Bytes()
}

func appendVideoEncoderConfiguration(e *Envelope, tag string, meta *StreamMeta) {
	// empty `RateControl` important for UniFi Protect
	codec := meta.video()
	e.Appendf(`<tt:%s token="vec">
		<tt:Name>VEC</tt:Name>
        <tt:UseCount>1</tt:UseCount>
		<tt:Encoding>%s</tt:Encoding>
		<tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution>
        <tt:Quality>0</tt:Quality>
		<tt:RateControl><tt:FrameRateLimit>%d</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval><tt:BitrateLimit>%d</tt:BitrateLimit></tt:RateControl>`,
		tag, codec, meta.w(), meta.h(), meta.fps(), meta.bitrate())

	switch codec {
	case "H265":
		e.Append(`
        <tt:H265><tt:GovLength>10</tt:GovLength><tt:H265Profile>Main</tt:H265Profile></tt:H265>`)
	default:
		e.Append(`
        <tt:H264><tt:GovLength>10</tt:GovLength><tt:H264Profile>Main</tt:H264Profile></tt:H264>`)
	}

	e.Appendf(`
        <tt:SessionTimeout>PT10S</tt:SessionTimeout>
	</tt:%s>`, tag)
}

// Audio response builders

func GetAudioSourcesResponse(names []string, metas map[string]*StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetAudioSourcesResponse>`)
	for _, name := range names {
		if m := metas[name]; m != nil && m.HasAudio {
			e.Appendf(`<trt:AudioSources token="%s">
	<tt:Channels>%d</tt:Channels>
</trt:AudioSources>`, name, m.audioChannels())
		}
	}
	e.Append(`</trt:GetAudioSourcesResponse>`)
	return e.Bytes()
}

func GetAudioSourceConfigurationsResponse(names []string, metas map[string]*StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetAudioSourceConfigurationsResponse>`)
	for _, name := range names {
		if m := metas[name]; m != nil && m.HasAudio {
			appendAudioSourceConfiguration(e, "Configurations", name)
		}
	}
	e.Append(`</trt:GetAudioSourceConfigurationsResponse>`)
	return e.Bytes()
}

func GetAudioEncoderConfigurationsResponse(names []string, metas map[string]*StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetAudioEncoderConfigurationsResponse>`)
	for _, name := range names {
		if m := metas[name]; m != nil && m.HasAudio {
			appendAudioEncoderConfiguration(e, "Configurations", m)
		}
	}
	e.Append(`</trt:GetAudioEncoderConfigurationsResponse>`)
	return e.Bytes()
}

func GetAudioEncoderConfigurationResponse(meta *StreamMeta) []byte {
	e := NewEnvelope()
	e.Append(`<trt:GetAudioEncoderConfigurationResponse>`)
	appendAudioEncoderConfiguration(e, "AudioEncoderConfiguration", meta)
	e.Append(`</trt:GetAudioEncoderConfigurationResponse>`)
	return e.Bytes()
}

func appendAudioSourceConfiguration(e *Envelope, tag, name string) {
	// go2rtc name = ONVIF AudioSource token = AudioSourceConfiguration token
	e.Appendf(`<tt:%s token="%s" fixed="true">
	<tt:Name>ASC</tt:Name>
	<tt:UseCount>1</tt:UseCount>
	<tt:SourceToken>%s</tt:SourceToken>
</tt:%s>`, tag, name, name, tag)
}

func appendAudioEncoderConfiguration(e *Envelope, tag string, meta *StreamMeta) {
	e.Appendf(`<tt:%s token="aec">
	<tt:Name>AEC</tt:Name>
	<tt:UseCount>1</tt:UseCount>
	<tt:Encoding>%s</tt:Encoding>
	<tt:Bitrate>64</tt:Bitrate>
	<tt:SampleRate>%d</tt:SampleRate>
	<tt:SessionTimeout>PT10S</tt:SessionTimeout>
</tt:%s>`, tag, meta.audio(), meta.audioSampleRate(), tag)
}

func GetStreamUriResponse(uri string) []byte {
	e := NewEnvelope()
	e.Appendf(`<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri></trt:MediaUri></trt:GetStreamUriResponse>`, uri)
	return e.Bytes()
}

func GetSnapshotUriResponse(uri string) []byte {
	e := NewEnvelope()
	e.Appendf(`<trt:GetSnapshotUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri></trt:MediaUri></trt:GetSnapshotUriResponse>`, uri)
	return e.Bytes()
}

func GetScopesResponse(meta *StreamMeta) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetScopesResponse>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/name/%s</tt:ScopeItem></tds:Scopes>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/hardware/%s</tt:ScopeItem></tds:Scopes>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/Profile/Streaming</tt:ScopeItem></tds:Scopes>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/type/Network_Video_Transmitter</tt:ScopeItem></tds:Scopes>
</tds:GetScopesResponse>`, meta.modelName("go2rtc"), meta.modelName("go2rtc"))
	return e.Bytes()
}

func StaticResponse(operation string) []byte {
	switch operation {
	case DeviceGetSystemDateAndTime:
		return GetSystemDateAndTimeResponse()
	}

	e := NewEnvelope()
	e.Append(responses[operation])
	return e.Bytes()
}

var responses = map[string]string{
	ServiceGetServiceCapabilities: `<trt:GetServiceCapabilitiesResponse>
	<trt:Capabilities SnapshotUri="true" Rotation="false" VideoSourceMode="false" OSD="false" TemporaryOSDText="false" EXICompression="false">
		<trt:StreamingCapabilities RTPMulticast="false" RTP_TCP="false" RTP_RTSP_TCP="true" NonAggregateControl="false" NoRTSPStreaming="false" />
	</trt:Capabilities>
</trt:GetServiceCapabilitiesResponse>`,

	DeviceGetDiscoveryMode:         `<tds:GetDiscoveryModeResponse><tds:DiscoveryMode>Discoverable</tds:DiscoveryMode></tds:GetDiscoveryModeResponse>`,
	DeviceGetDNS:                   `<tds:GetDNSResponse><tds:DNSInformation /></tds:GetDNSResponse>`,
	DeviceGetHostname:              `<tds:GetHostnameResponse><tds:HostnameInformation /></tds:GetHostnameResponse>`,
	DeviceGetNetworkDefaultGateway: `<tds:GetNetworkDefaultGatewayResponse><tds:NetworkGateway /></tds:GetNetworkDefaultGatewayResponse>`,
	DeviceGetNTP:                   `<tds:GetNTPResponse><tds:NTPInformation /></tds:GetNTPResponse>`,
	DeviceSetSystemDateAndTime:     `<tds:SetSystemDateAndTimeResponse />`,
	DeviceSystemReboot:             `<tds:SystemRebootResponse><tds:Message>OK</tds:Message></tds:SystemRebootResponse>`,

	// DeviceGetNetworkInterfaces is handled dynamically (per-stream MAC address).
	DeviceGetNetworkProtocols:  `<tds:GetNetworkProtocolsResponse />`,

	MediaGetVideoEncoderConfigurationOptions: `<trt:GetVideoEncoderConfigurationOptionsResponse>
   <trt:Options>
       <tt:QualityRange><tt:Min>1</tt:Min><tt:Max>6</tt:Max></tt:QualityRange>
	   <tt:H264>
		   <tt:ResolutionsAvailable><tt:Width>1920</tt:Width><tt:Height>1080</tt:Height></tt:ResolutionsAvailable>
		   <tt:GovLengthRange><tt:Min>0</tt:Min><tt:Max>100</tt:Max></tt:GovLengthRange>
		   <tt:FrameRateRange><tt:Min>1</tt:Min><tt:Max>30</tt:Max></tt:FrameRateRange>
		   <tt:EncodingIntervalRange><tt:Min>1</tt:Min><tt:Max>100</tt:Max></tt:EncodingIntervalRange>
           <tt:H264ProfilesSupported>Main</tt:H264ProfilesSupported>
	   </tt:H264>
   </trt:Options>
</trt:GetVideoEncoderConfigurationOptionsResponse>`,
}
