package drm

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCPIXParser(t *testing.T) {
	testCases := []struct {
		desc            string
		file            string
		expectedErr     bool
		wantedContentID string
		wantedNrKeys    int
		wantedNrDRMs    int
	}{
		{
			desc:            "1 key, CBCS",
			file:            "testdata/cpix_1key_cbcs_test.xml",
			wantedContentID: "livesim2-0001",
			wantedNrKeys:    1,
			wantedNrDRMs:    2,
		},
		{
			desc:            "2 keys, CBCS, with usage rules for video and audio",
			file:            "testdata/cpix_2keys_cbcs_test.xml",
			wantedContentID: "livesim2-0002",
			wantedNrKeys:    2,
			wantedNrDRMs:    2,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			data, err := os.ReadFile(tc.file)
			assert.NoError(t, err)
			pd, err := ParseCPIX(data)
			if tc.expectedErr && err == nil {
				t.Error("expected an error, got nil")
			}
			if !tc.expectedErr && err != nil {
				t.Errorf("expected nil error, got %v", err)
			}
			require.Equal(t, tc.wantedContentID, pd.ContentID)
			require.Equal(t, tc.wantedNrKeys, len(pd.ContentKeys))
			require.Equal(t, tc.wantedNrDRMs*tc.wantedNrKeys, len(pd.DRMSystems))
			require.Equal(t, tc.wantedNrKeys, len(pd.UsageRules))
		})
	}
}
