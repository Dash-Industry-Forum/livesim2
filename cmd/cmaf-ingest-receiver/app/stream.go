package app

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"

	"github.com/Dash-Industry-Forum/livesim2/pkg/cmaf"
)

var mpdRegexp = regexp.MustCompile(`^\/(.*)\/[^\/]+\.mpd$`)
var streamsRegexp = regexp.MustCompile(`^\/(.*)\/Streams\((.*)(\.cmf[vatm])\)$`)
var segmentRegexp = regexp.MustCompile(`^\/((.*)\/)?([^\/]+)?\/([^\/]+)(\.cmf[vatm])$`)

var mimeTypeFromMediaType = map[string]string{
	"video": "video/mp4",
	"audio": "audio/mp4",
	"text":  "application/mp4",
}

var extFromMediaType = map[string]string{
	"video": ".cmfv",
	"audio": ".cmfa",
	"text":  ".cmft",
}

// stream is used to represent a stream of segments (a xCMAF track).
// It contains the channel an track names.
type stream struct {
	chName    string
	trName    string
	ext       string
	mediaType string
	chDir     string
	trDir     string
}

// id returns a unique identifier for the stream.
func (s stream) id() string {
	return fmt.Sprintf("%s/%s", s.chName, s.trName)
}

func matchMPD(path string) (chName string, ok bool) {
	matches := mpdRegexp.FindStringSubmatch(path)
	if len(matches) == 0 {
		return "", false
	}
	chName = matches[1]
	return filepath.Join(chName), true
}

func findStreamMatch(storagePath, path string) (stream, bool) {
	str := stream{}
	var err error
	matches := streamsRegexp.FindStringSubmatch(path)
	if len(matches) > 0 {
		str.chName = matches[1]
		str.trName = matches[2]
		str.ext = matches[3]
	} else {
		matches = segmentRegexp.FindStringSubmatch(path)
		if len(matches) > 1 {
			str.chName = matches[2]
			str.trName = matches[3]
			str.ext = matches[5]
		}
	}
	if len(matches) == 0 {
		return str, false
	}
	str.mediaType, err = cmaf.ContentTypeFromCMAFExtension(str.ext)
	if err != nil {
		return str, false
	}
	str.chDir = filepath.Join(storagePath, str.chName)
	str.trDir = filepath.Join(str.chDir, str.trName)
	slog.Debug("Found stream", "stream", str.id())
	return str, true
}
