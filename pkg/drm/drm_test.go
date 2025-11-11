package drm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadFullConfig(t *testing.T) {
	drmConfig := "testdata/drm_config_test.json"
	drmCfgs, err := ReadDrmConfig(drmConfig)
	require.NoError(t, err)
	require.NotNil(t, drmCfgs)
	require.Equal(t, "0.5", drmCfgs.Version)
	cfg, ok := drmCfgs.Map["EZDRM-1-key-cbcs-test"]
	require.True(t, ok)
	require.NotNil(t, cfg)
	require.Equal(t, 1, len(cfg.CPIXData.ContentKeys))
	require.Equal(t, 3, len(cfg.CPIXData.DRMSystems))
	require.Equal(t, 1, len(cfg.CPIXData.UsageRules))
	require.Equal(t, "livesim2-0001", cfg.CPIXData.ContentID)

	// Check that CertificateURL is properly read for FairPlay
	fairplayURL, ok := cfg.URLs["fairplay"]
	require.True(t, ok)
	require.NotNil(t, fairplayURL)
	require.Equal(t, "https://us-dev.ezdrm.com/fps", fairplayURL.LaURL)
	require.Equal(t, "https://na-fps.ezdrm.com/demo/video/eleisure.cer", fairplayURL.CertificateURL)

	// Check PlayReady has both LaURL and CertificateURL
	playreadyURL, ok := cfg.URLs["playready"]
	require.True(t, ok)
	require.NotNil(t, playreadyURL)
	require.Equal(t, "https://playready.ezdrm.com/cency/preauth.asp?pX=FFFFFF", playreadyURL.LaURL)
	require.Equal(t, "https://na-wv.ezdrm.com/demo/video/eleisure.cer", playreadyURL.CertificateURL)

	// Check Widevine has LaURL but empty CertificateURL
	widevineURL, ok := cfg.URLs["widevine"]
	require.True(t, ok)
	require.NotNil(t, widevineURL)
	require.Equal(t, "https://widevine-dash.ezdrm.com/proxy?pX=FFFFFF", widevineURL.LaURL)
	require.Equal(t, "", widevineURL.CertificateURL)
}

func TestToUUIDStr(t *testing.T) {
	testCases := []struct {
		desc    string
		input   []byte
		want    string
		wantErr bool
	}{
		{
			desc:    "valid input",
			input:   []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
			want:    "00010203-0405-0607-0809-0a0b0c0d0e0f",
			wantErr: false,
		},
		{
			desc:    "wrong length input",
			input:   []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
			wantErr: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := ToUUIDStr(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
