package cmaf

import "fmt"

const (
	CMAFVideoExtension = ".cmfv"
	CMAFAudioExtension = ".cmfa"
	CMAFTextExtension  = ".cmft"
	CMAFMetaExtension  = ".cmfm"
)

// ContentTypeFromCMAFExtension returns the content type of a CMAF file based on file extension ext.
func ContentTypeFromCMAFExtension(ext string) (string, error) {
	switch ext {
	case CMAFVideoExtension:
		return "video", nil
	case CMAFAudioExtension:
		return "audio", nil
	case CMAFTextExtension:
		return "text", nil
	case CMAFMetaExtension:
		return "metadata", nil
	default:
		return "", fmt.Errorf("unknown CMAF file extension %s", ext)
	}
}

// CMAFExtensionFromContentType returns the file extension of a CMAF file based on contentType.
func CMAFExtensionFromContentType(contentType string) (string, error) {
	switch contentType {
	case "video":
		return CMAFVideoExtension, nil
	case "audio":
		return CMAFAudioExtension, nil
	case "text":
		return CMAFTextExtension, nil
	case "metadata":
		return CMAFMetaExtension, nil
	default:
		return "", fmt.Errorf("unknown CMAF contentType %s", contentType)
	}
}
