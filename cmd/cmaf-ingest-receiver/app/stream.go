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

func findStreamMatch(storagePath, path string) (stream, bool, error) {
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
		return str, false, nil
	}
	str.mediaType, err = cmaf.ContentTypeFromCMAFExtension(str.ext)
	if err != nil {
		return str, false, nil
	}
	str.chDir, err = joinAbsPathSecurely(storagePath, str.chName)
	if err != nil {
		return str, false, fmt.Errorf("insecure channel path: %w", err)
	}
	str.trDir, err = joinAbsPathSecurely(str.chDir, str.trName)
	if err != nil {
		return str, false, fmt.Errorf("insecure track path: %w", err)
	}
	slog.Debug("Found stream", "stream", str.id())
	return str, true, nil
}
