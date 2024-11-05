package drm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type DrmConfig struct {
	Version  string              `json:"version"`
	Packages []*Package          `json:"packages"`
	Map      map[string]*Package `json:"-"`
}

type Package struct {
	// Name is used to identify this configuration.
	Name string `json:"name"`
	// Desc is a description of the configuration.
	Desc string `json:"desc,omitempty"`
	// CPIXFile is the path to the CPIX file.
	CPIXFile string `json:"cpixFile"`
	// URLs to license servers for each DRM system.
	URLs map[string]LicenseURL `json:"licenseURLs"`
	// CPIXData is the parsed CPIX data.
	CPIXData CPIXData `json:"cpixdata"`
}

type LicenseURL struct {
	// LaURL is the URL to the license server.
	LaURL string `json:"laURL"`
	// CertificateURL is the URL to the certificate server (for FPS)
	CertificateURL string `json:"certURL,omitempty"`
}

// ReadDrmConfig reads a JSON file containing DRM configuration.
func ReadDrmConfig(path string) (*DrmConfig, error) {
	drmCfgs := DrmConfig{
		Map: make(map[string]*Package),
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	err = json.Unmarshal(raw, &drmCfgs)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	for _, cfg := range drmCfgs.Packages {
		cpixPath := cfg.CPIXFile
		if cpixPath == "" {
			return nil, fmt.Errorf("cpixFile is required")
		}

		if !filepath.IsAbs(cpixPath) {
			dir := filepath.Dir(path)
			cpixPath = filepath.Join(dir, cpixPath)
		}
		cpixRaw, err := os.ReadFile(cpixPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CPIX file: %w", err)
		}
		cpixData, err := ParseCPIX(cpixRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse CPIX: %w", err)
		}
		cfg.CPIXData = *cpixData
		drmCfgs.Map[cfg.Name] = cfg
	}
	return &drmCfgs, nil
}

func (dc *DrmConfig) GetConfig(name string) *Package {
	for _, cfg := range dc.Packages {
		if cfg.Name == name {
			return cfg
		}
	}
	return nil
}

func ToUUIDStr(raw []byte) (string, error) {
	if len(raw) != 16 {
		return "", fmt.Errorf("invalid UUID length: %d", len(raw))
	}
	return fmt.Sprintf("%8x-%4x-%4x-%4x-%12x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]), nil
}
