package app

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Channels []ChannelConfig `json:"channels"`
}

type ChannelConfig struct {
	Name                  string `json:"name"`
	StartNr               int    `json:"startNr"`
	AuthUser              string `json:"authUser"`
	AuthPswd              string `json:"authPassword"`
	TimeShiftBufferDepthS int    `json:"timeShiftBufferDepthS"`
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
