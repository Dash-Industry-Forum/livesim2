package app

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config provides configuration for channels.
// If DefaultUsr or DefaultPswd is set,
// it will be used as default for all channels, meaning that
// no password less channels are allowed.
// Drop means that a channel will be dropped and not processed.
type Config struct {
	DefaultUser string          `json:"defaultUser"`
	DefaultPswd string          `json:"defaultPassword"`
	Channels    []ChannelConfig `json:"channels"`
}

func GetEmptyConfig() *Config {
	return &Config{
		Channels: []ChannelConfig{},
	}
}

type ChannelConfig struct {
	Name                  string                 `json:"name"`
	StartNr               int                    `json:"startNr"`
	AuthUser              string                 `json:"authUser"`
	AuthPswd              string                 `json:"authPassword"`
	TimeShiftBufferDepthS uint32                 `json:"timeShiftBufferDepthS"`
	ReceiveNrRawSegments  uint32                 `json:"receiveNrRawSegments"`
	Reps                  []RepresentationConfig `json:"reps"`
	Ignore                bool                   `json:"ignore,omitempty"`
}

// RepresentationConfig provides configuration for a representation.
// If Name matches an incoming stream, non-zero values here will
// override the values from the incoming stream.
// Ignore means that the representation should be ignored.
type RepresentationConfig struct {
	Name        string `json:"name"`
	Language    string `json:"language"`
	Role        string `json:"role"`
	DisplayName string `json:"displayName"`
	Bitrate     uint32 `json:"bitrate,omitempty"`
	Ignore      bool   `json:"ignore,omitempty"`
}

func (cc *ChannelConfig) GetRepConfig(name string) (*RepresentationConfig, bool) {
	for i := range cc.Reps {
		if cc.Reps[i].Name == name {
			return &cc.Reps[i], true
		}
	}
	return nil, false
}

func readConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	err = json.Unmarshal(data, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return cfg, nil
}
