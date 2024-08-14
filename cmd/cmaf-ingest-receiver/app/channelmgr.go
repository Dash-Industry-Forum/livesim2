package app

import (
	"context"
	"log/slog"
	"sync"
)

// ChannelMgr is a manager for channel objects.
// The key is the channel name that must be unique.
type ChannelMgr struct {
	mu                           sync.RWMutex
	cfg                          *Config
	defaultTimeShiftBufferDepthS int
	channels                     map[string]*channel
}

func NewChannelMgr(cfg *Config, defaultTimeShiftBufferDepthS int) *ChannelMgr {
	slog.Debug("Creating ChannelMgr", "timeShiftBufferDepthS", defaultTimeShiftBufferDepthS)
	return &ChannelMgr{
		cfg:                          cfg,
		defaultTimeShiftBufferDepthS: defaultTimeShiftBufferDepthS,
		channels:                     make(map[string]*channel),
	}
}

func (cm *ChannelMgr) AddChannel(ctx context.Context, chName, chDir string) {
	cm.mu.Lock()

	chCfg := ChannelConfig{
		Name: chName,
	}
	if cm.cfg != nil {
		for _, cfg := range cm.cfg.Channels {
			if cfg.Name == chName {
				chCfg = cfg
				break
			}
		}
	}
	if chCfg.TimeShiftBufferDepthS == 0 {
		chCfg.TimeShiftBufferDepthS = cm.defaultTimeShiftBufferDepthS
	}
	cm.channels[chName] = newChannel(ctx, chCfg, chDir)
	cm.mu.Unlock()
}

func (cm *ChannelMgr) GetChannel(chName string) (*channel, bool) {
	cm.mu.RLock()
	fs, ok := cm.channels[chName]
	cm.mu.RUnlock()
	return fs, ok
}
