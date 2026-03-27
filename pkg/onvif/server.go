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
	Width    int    // video width in pixels; 0 → 1920
	Height   int    // video height in pixels; 0 → 1080
	FPS      int    // frames per second; 0 → 30
	Bitrate  int    // bitrate limit in kbps; 0 → 8192
	Video    string // ONVIF encoding name ("H264" or "H265"); "" → "H264"
	HasAudio bool   // whether the stream carries an audio track
}

// nil-safe accessor methods

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

func GetCapabilitiesResponse(host string) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetCapabilitiesResponse>
	<tds:Capabilities>
		<tt:Device>
			<tt:XAddr>http://%s/onvif/device_service</tt:XAddr>
		</tt:Device>
		<tt:Media>
			<tt:XAddr>http://%s/onvif/media_service</tt:XAddr>
			<tt:StreamingCapabilities>
				<tt:RTPMulticast>false</tt:RTPMulticast>
				<tt:RTP_TCP>false</tt:RTP_TCP>
				<tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP>
			</tt:StreamingCapabilities>
		</tt:Media>
	</tds:Capabilities>
</tds:GetCapabilitiesResponse>`, host, host)
	return e.Bytes()
}

func GetServicesResponse(host string) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetServicesResponse>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace>
		<tds:XAddr>http://%s/onvif/device_service</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>5</tt:Minor></tds:Version>
	</tds:Service>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace>
		<tds:XAddr>http://%s/onvif/media_service</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>5</tt:Minor></tds:Version>
	</tds:Service>
</tds:GetServicesResponse>`, host, host)
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

func GetDeviceInformationResponse(manuf, model, firmware, serial string) []byte {
	e := NewEnvelope()
	e.Appendf(`<tds:GetDeviceInformationResponse>
	<tds:Manufacturer>%s</tds:Manufacturer>
	<tds:Model>%s</tds:Model>
	<tds:FirmwareVersion>%s</tds:FirmwareVersion>
	<tds:SerialNumber>%s</tds:SerialNumber>
	<tds:HardwareId>1.00</tds:HardwareId>
</tds:GetDeviceInformationResponse>`, manuf, model, firmware, serial)
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
	e.Appendf(`<tt:Name>%s</tt:Name>`, name)
	appendVideoSourceConfiguration(e, "VideoSourceConfiguration", name, meta)
	appendVideoEncoderConfiguration(e, "VideoEncoderConfiguration", meta)
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

	DeviceGetNetworkInterfaces: `<tds:GetNetworkInterfacesResponse />`,
	DeviceGetNetworkProtocols:  `<tds:GetNetworkProtocolsResponse />`,
	DeviceGetScopes: `<tds:GetScopesResponse>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/name/go2rtc</tt:ScopeItem></tds:Scopes>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/location/github</tt:ScopeItem></tds:Scopes>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/Profile/Streaming</tt:ScopeItem></tds:Scopes>
	<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/type/Network_Video_Transmitter</tt:ScopeItem></tds:Scopes>
</tds:GetScopesResponse>`,

	MediaGetAudioEncoderConfigurations: `<trt:GetAudioEncoderConfigurationsResponse />`,
	MediaGetAudioSources:               `<trt:GetAudioSourcesResponse />`,
	MediaGetAudioSourceConfigurations:  `<trt:GetAudioSourceConfigurationsResponse />`,

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
