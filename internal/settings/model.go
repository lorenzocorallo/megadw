package settings

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	DefaultIncompleteRoot = "/mnt/media/downloads/mega/incomplete"
	DefaultCompleteRoot   = "/mnt/media/downloads/mega/complete"
	DefaultSegmentSize    = int64(8 << 20)
)

type Settings struct {
	Paths     PathsSettings    `json:"paths"`
	Downloads DownloadSettings `json:"downloads"`
	Network   NetworkSettings  `json:"network"`
	UI        UISettings       `json:"ui"`
}

type PathsSettings struct {
	IncompleteRoot string `json:"incompleteRoot"`
	CompleteRoot   string `json:"completeRoot"`
}

type DownloadSettings struct {
	AutoStart                        bool   `json:"autoStart"`
	SegmentSizeBytes                 int64  `json:"segmentSizeBytes"`
	WorkersPerFile                   int    `json:"workersPerFile"`
	MaxActiveFiles                   int    `json:"maxActiveFiles"`
	MaxGlobalWorkers                 int    `json:"maxGlobalWorkers"`
	GlobalSpeedLimitBytesPerSecond   int64  `json:"globalSpeedLimitBytesPerSecond"`
	PerJobDefaultLimitBytesPerSecond int64  `json:"perJobDefaultLimitBytesPerSecond"`
	ConflictPolicy                   string `json:"conflictPolicy"`
	CheckpointIntervalMs             int64  `json:"checkpointIntervalMs"`
	CheckpointBytes                  int64  `json:"checkpointBytes"`
	NormalRetryLimit                 int    `json:"normalRetryLimit"`
}

type NetworkSettings struct {
	ConnectTimeoutSeconds        int `json:"connectTimeoutSeconds"`
	ResponseHeaderTimeoutSeconds int `json:"responseHeaderTimeoutSeconds"`
	ReadIdleTimeoutSeconds       int `json:"readIdleTimeoutSeconds"`
}

type UISettings struct {
	Theme  string `json:"theme"`
	Locale string `json:"locale"`
}

func Default() Settings {
	return Settings{
		Paths: PathsSettings{IncompleteRoot: DefaultIncompleteRoot, CompleteRoot: DefaultCompleteRoot},
		Downloads: DownloadSettings{
			AutoStart:                        true,
			SegmentSizeBytes:                 DefaultSegmentSize,
			WorkersPerFile:                   4,
			MaxActiveFiles:                   2,
			MaxGlobalWorkers:                 8,
			GlobalSpeedLimitBytesPerSecond:   0,
			PerJobDefaultLimitBytesPerSecond: 0,
			ConflictPolicy:                   "rename",
			CheckpointIntervalMs:             2000,
			CheckpointBytes:                  256 << 20,
			NormalRetryLimit:                 5,
		},
		Network: NetworkSettings{ConnectTimeoutSeconds: 15, ResponseHeaderTimeoutSeconds: 30, ReadIdleTimeoutSeconds: 90},
		UI:      UISettings{Theme: "system", Locale: "en"},
	}
}

func (s Settings) Validate() error {
	var problems []string
	checkRoot := func(name, value string) {
		if value == "" {
			problems = append(problems, name+" is required")
			return
		}
		if strings.IndexByte(value, 0) >= 0 {
			problems = append(problems, name+" contains NUL")
			return
		}
		if !filepath.IsAbs(value) {
			problems = append(problems, name+" must be absolute")
			return
		}
		if filepath.Clean(value) == string(filepath.Separator) {
			problems = append(problems, name+" must not be the filesystem root")
		}
	}
	checkRoot("paths.incompleteRoot", s.Paths.IncompleteRoot)
	checkRoot("paths.completeRoot", s.Paths.CompleteRoot)

	if s.Downloads.SegmentSizeBytes < 1<<20 || s.Downloads.SegmentSizeBytes > 64<<20 {
		problems = append(problems, "downloads.segmentSizeBytes must be between 1048576 and 67108864")
	}
	if s.Downloads.WorkersPerFile < 1 || s.Downloads.WorkersPerFile > 16 {
		problems = append(problems, "downloads.workersPerFile must be between 1 and 16")
	}
	if s.Downloads.MaxActiveFiles < 1 || s.Downloads.MaxActiveFiles > 16 {
		problems = append(problems, "downloads.maxActiveFiles must be between 1 and 16")
	}
	if s.Downloads.MaxGlobalWorkers < 1 || s.Downloads.MaxGlobalWorkers > 64 {
		problems = append(problems, "downloads.maxGlobalWorkers must be between 1 and 64")
	}
	if s.Downloads.GlobalSpeedLimitBytesPerSecond < 0 || s.Downloads.PerJobDefaultLimitBytesPerSecond < 0 {
		problems = append(problems, "download speed limits must not be negative")
	}
	switch s.Downloads.ConflictPolicy {
	case "rename", "overwrite", "fail":
	default:
		problems = append(problems, "downloads.conflictPolicy must be rename, overwrite, or fail")
	}
	if s.Downloads.CheckpointIntervalMs < 100 || s.Downloads.CheckpointIntervalMs > 60_000 {
		problems = append(problems, "downloads.checkpointIntervalMs must be between 100 and 60000")
	}
	if s.Downloads.CheckpointBytes < 1<<20 || s.Downloads.CheckpointBytes > 1<<40 {
		problems = append(problems, "downloads.checkpointBytes is outside the safe range")
	}
	if s.Downloads.NormalRetryLimit < 0 || s.Downloads.NormalRetryLimit > 20 {
		problems = append(problems, "downloads.normalRetryLimit must be between 0 and 20")
	}
	if s.Network.ConnectTimeoutSeconds < 1 || s.Network.ConnectTimeoutSeconds > 300 {
		problems = append(problems, "network.connectTimeoutSeconds must be between 1 and 300")
	}
	if s.Network.ResponseHeaderTimeoutSeconds < 1 || s.Network.ResponseHeaderTimeoutSeconds > 600 {
		problems = append(problems, "network.responseHeaderTimeoutSeconds must be between 1 and 600")
	}
	if s.Network.ReadIdleTimeoutSeconds < 1 || s.Network.ReadIdleTimeoutSeconds > 3600 {
		problems = append(problems, "network.readIdleTimeoutSeconds must be between 1 and 3600")
	}
	if s.UI.Theme != "light" && s.UI.Theme != "dark" && s.UI.Theme != "system" {
		problems = append(problems, "ui.theme must be light, dark, or system")
	}
	if s.UI.Locale != "en" {
		problems = append(problems, "ui.locale must be en")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid settings: %s", strings.Join(problems, "; "))
	}
	return nil
}

func marshalSections(value Settings) (map[string][]byte, error) {
	sections := map[string]any{
		"paths":     value.Paths,
		"downloads": value.Downloads,
		"network":   value.Network,
		"ui":        value.UI,
	}
	result := make(map[string][]byte, len(sections))
	for key, section := range sections {
		encoded, err := json.Marshal(section)
		if err != nil {
			return nil, fmt.Errorf("marshal settings.%s: %w", key, err)
		}
		result[key] = encoded
	}
	return result, nil
}
