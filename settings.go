package main

import "zpic/internal/compress"

type compressionPreset struct {
	name    string
	quality float64
	png     compress.PNGOptions
}

var compressionPresets = []compressionPreset{
	{"体积优先", 60, compress.PNGOptions{MinQuality: 40, MaxQuality: 65, Colors: 128, Speed: 3}},
	{"均衡", 80, compress.DefaultPNGOptions()},
	{"画质优先", 92, compress.PNGOptions{MinQuality: 85, MaxQuality: 100, Colors: 256, Speed: 1}},
}

func (s *application) setScheme(name string) {
	if s.busy {
		return
	}
	if name == "自定义" {
		s.scheme = name
		return
	}
	for _, preset := range compressionPresets {
		if preset.name == name {
			changed := s.quality != preset.quality || s.png != preset.png
			s.scheme, s.quality, s.png = name, preset.quality, preset.png
			if changed {
				s.invalidateResult()
			}
			return
		}
	}
}

func (s *application) setPNGOptions(options compress.PNGOptions) {
	if s.busy || options.Validate() != nil {
		return
	}
	if options == (compress.PNGOptions{}) {
		options = compress.DefaultPNGOptions()
	}
	if s.png == options {
		return
	}
	s.png, s.scheme = options, "自定义"
	s.invalidateResult()
}

// Capture all settings on the UI thread before launching compression.
func (s *application) compressionOptions() compress.Options {
	return compress.Options{Quality: int(s.quality), PNG: s.png}
}
