package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSegStatusCodes(t *testing.T) {
	key := "statuscode"
	cases := []struct {
		desc    string
		val     string
		wantErr string
		want    []SegStatusCodes
	}{
		{
			desc:    "empty",
			val:     "",
			wantErr: `val="" for key "statuscode" is too short`,
		},
		{
			desc: "404 on first video packet",
			val:  "[{cycle:30, rsq:0, code:404, rep:video}]",
			want: []SegStatusCodes{
				{Cycle: 30, Rsq: 0, Code: 404, Reps: []string{"video"}},
			},
		},
		{
			desc: "* for reps",
			val:  "[{cycle:30, rsq:2, code:404, rep:*}]",
			want: []SegStatusCodes{
				{Cycle: 30, Rsq: 2, Code: 404, Reps: nil},
			},
		},
		{
			desc: "no reps",
			val:  "[{cycle:30, rsq:1, code:404}]",
			want: []SegStatusCodes{
				{Cycle: 30, Rsq: 1, Code: 404, Reps: nil},
			},
		},
		{
			desc:    "bad code",
			val:     "[{cycle:30, rsq:2, code:600, rep:*}]",
			wantErr: `val="[{cycle:30, rsq:2, code:600, rep:*}]" for key "statuscode" is not a valid. code is not in range 400-599`,
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			s := strConvAccErr{}
			got := s.ParseSegStatusCodes(key, c.val)
			if c.wantErr != "" {
				require.Equal(t, c.wantErr, s.err.Error())
				return
			}
			require.NoError(t, s.err)
			require.Equal(t, c.want, got)
		})
	}
}
