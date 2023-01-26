package internal

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
)

const (
	MPDListFile = "mpdlist.json"
)

// MPDData stores mpd name to original URI relation.
type MPDData struct {
	Name    string `json:"name"`
	OrigURI string `json:"originURI"`
	MPDStr  string `json:"-"`
}

// WriteMPDData to file on disk.
func WriteMPDData(dirPath string, name, uri string) error {
	filePath := path.Join(dirPath, MPDListFile)
	_, err := os.Stat(filePath)
	exists := true
	if os.IsNotExist(err) {
		exists = false
	}
	var mpds []MPDData
	if exists {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		err = json.Unmarshal(data, &mpds)
		if err != nil {
			return err
		}
	}
	mpds = append(mpds, MPDData{name, uri, ""})
	outData, err := json.MarshalIndent(mpds, "", "  ")
	if err != nil {
		return err
	}
	err = os.WriteFile(filePath, outData, 0644)
	if err != nil {
		return err
	}
	return nil
}

// ReadMPDData for MPD from file on disk.
func ReadMPDData(vodFS fs.FS, mpdPath string) MPDData {
	assetPath, mpdName := path.Split(mpdPath)
	if assetPath != "" {
		assetPath = assetPath[:len(assetPath)-1]
	}
	md := MPDData{Name: mpdName}
	mpdListPath := path.Join(assetPath, MPDListFile)
	data, err := fs.ReadFile(vodFS, mpdListPath)
	if err != nil {
		return md
	}
	var mds []MPDData
	err = json.Unmarshal(data, &mds)
	if err != nil {
		return md
	}
	for _, m := range mds {
		if m.Name == mpdName {
			return m
		}
	}
	return md
}
