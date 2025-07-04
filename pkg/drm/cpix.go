package drm

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/beevik/etree"
)

// CPIXData represents the data needed for encrypting media files.
type CPIXData struct {
	ContentID   string                `json:"contentId"`
	ContentKeys []ContentKey          `json:"contentKeys"`
	DRMSystems  []DRMSystem           `json:"drmSystems"`
	UsageRules  []ContentKeyUsageRule `json:"usageRules"`
}

func (cd *CPIXData) GetContentKey(contentType string) (ContentKey, error) {
	if len(cd.ContentKeys) == 1 {
		return cd.ContentKeys[0], nil
	}
	var keyID mp4.UUID
	for _, ur := range cd.UsageRules {
		if strings.ToLower(ur.IntendedTrackType) == contentType {
			keyID = ur.KeyID
			break
		}
	}
	if len(keyID) == 0 {
		return ContentKey{}, fmt.Errorf("no key found for content type %q", contentType)
	}

	for _, ck := range cd.ContentKeys {
		if bytes.Equal([]byte(ck.KeyID), []byte(keyID)) {
			return ck, nil
		}
	}
	return ContentKey{}, fmt.Errorf("no key found for content type %q", contentType)
}

type ContentKey struct {
	// ExplicitIV is the initialization vector (when specified) (16 bytes)
	ExplicitIV []byte `json:"explicitIV"`
	// KeyID is the content key ID (16 bytes)
	KeyID mp4.UUID `json:"kid"`
	// Key is the content key (16 bytes)
	Key []byte `json:"-"`
	// CommonEncryptionScheme should be cenc or cbcs
	CommonEncryptionScheme string `json:"commonEncryptionScheme"`
}

type DRMSystem struct {
	// SystemID is the DRM system ID
	SystemID string `json:"systemId"`
	// KeyID is the key ID for the content key ID
	KeyID mp4.UUID `json:"kid"`
	// PSSH is the Protection System Specific Header base64 encoded
	PSSH string `json:"pssh"`
	// SmoothStreamingProtectionHeaderData is base64 encoded
	SmoothStreamingProtectionHeaderData string `json:"smoothStreamingProtectionHeaderData,omitempty"`
}

// ContentKeyUsageRule represents a usage rule for a content key
// Typically what tracks or media the key is intended for
type ContentKeyUsageRule struct {
	KeyID             mp4.UUID `json:"kid"`
	IntendedTrackType string   `json:"intendedTrackType"`
}

// ParseCPIX parses a CPIX XML document and returns a CPIXData struct.
// It should provide all information needed for encrypting media files.
func ParseCPIX(raw []byte) (*CPIXData, error) {
	d := etree.NewDocument()
	err := d.ReadFromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CPIX XML: %w", err)
	}
	root := d.Root()
	if root.Tag != "CPIX" {
		return nil, fmt.Errorf("unexpected root element: %s", root.Tag)
	}
	cpd := CPIXData{}
	cpd.ContentID = getAttrValue(root, "contentId")
	keyElems := root.FindElements("./ContentKeyList/ContentKey")
	for _, ke := range keyElems {
		kc := ContentKey{}
		kc.KeyID, err = mp4.NewUUIDFromString(getAttrValue(ke, "kid"))
		if err != nil {
			return nil, fmt.Errorf("failed to parse key ID: %w", err)
		}
		kc.CommonEncryptionScheme = getAttrValue(ke, "commonEncryptionScheme")
		ivBase64 := getAttrValue(ke, "explicitIV")
		if ivBase64 != "" {
			iv, err := base64.StdEncoding.DecodeString(ivBase64)
			if err != nil {
				return nil, fmt.Errorf("failed to decode base64 IV: %w", err)
			}
			kc.ExplicitIV = iv
		}
		pv := ke.FindElement("./Data/Secret/PlainValue")
		if pv != nil {
			kc.Key, err = base64.StdEncoding.DecodeString(pv.Text())
			if err != nil {
				return nil, fmt.Errorf("failed to decode base64 key %q: %w", pv.Text(), err)
			}
		}
		cpd.ContentKeys = append(cpd.ContentKeys, kc)
	}
	drmSystems := root.FindElements("./DRMSystemList/DRMSystem")
	for _, ds := range drmSystems {
		drm := DRMSystem{}
		drm.SystemID = getAttrValue(ds, "systemId")
		drm.KeyID, err = mp4.NewUUIDFromString(getAttrValue(ds, "kid"))
		if err != nil {
			return nil, fmt.Errorf("failed to parse key ID: %w", err)
		}
		pssh := ds.FindElement("./PSSH")
		if pssh != nil {
			drm.PSSH = pssh.Text()
		}
		mssProtData := ds.FindElement("./SmoothStreamingProtectionHeaderData")
		if mssProtData != nil {
			drm.SmoothStreamingProtectionHeaderData = mssProtData.Text()
		}
		cpd.DRMSystems = append(cpd.DRMSystems, drm)
	}
	usageRules := root.FindElements("./ContentKeyUsageRuleList/ContentKeyUsageRule")
	for _, ur := range usageRules {
		rule := ContentKeyUsageRule{}
		rule.KeyID, err = mp4.NewUUIDFromString(getAttrValue(ur, "kid"))
		if err != nil {
			return nil, fmt.Errorf("failed to parse key ID: %w", err)
		}
		rule.IntendedTrackType = getAttrValue(ur, "intendedTrackType")
		cpd.UsageRules = append(cpd.UsageRules, rule)
	}

	return &cpd, nil
}

// getAttrValue returns value if key exists, of empty string
func getAttrValue(e *etree.Element, key string) string {
	a := e.SelectAttr(key)
	if a == nil {
		return ""
	}
	return a.Value
}

// DrmNames maps DRM system IDs to human readable names
var DrmNames = map[string]string{
	"urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed": "widevine",
	"urn:uuid:9a04f079-9840-4286-ab92-e65be0885f95": "playready",
	"urn:uuid:94ce86fb-07ff-4f43-adb8-93d2fa968ca2": "fairplay",
}

// SystemIDs maps drm names to DRM system IDs
var SystemIDs = map[string]string{
	"widevine":  "urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed",
	"playready": "urn:uuid:9a04f079-9840-4286-ab92-e65be0885f95",
	"fairplay":  "urn:uuid:94ce86fb-07ff-4f43-adb8-93d2fa968ca2",
}

// ContentProtectionValues for DASH MPD depending on DRM system
var ContentProtectionValues = map[string]string{
	"urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed": "Widevine",
	"urn:uuid:9a04f079-9840-4286-ab92-e65be0885f95": "MSPR 2.0",
	"urn:uuid:94ce86fb-07ff-4f43-adb8-93d2fa968ca2": "Fairplay",
}
