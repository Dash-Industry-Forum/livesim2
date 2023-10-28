// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// urlGenHandlerFunc returns page for generating URLs
func (s *Server) urlGenHandlerFunc(w http.ResponseWriter, r *http.Request) {
	assets := make([]*asset, 0, len(s.assetMgr.assets))
	for _, a := range s.assetMgr.assets {
		assets = append(assets, a)
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].AssetPath < assets[j].AssetPath
	})
	fh := fullHost(s.Cfg.Host, r)
	playURL, err := createPlayURL(fh, s.Cfg.PlayURL)
	if err != nil {
		slog.Error("cannot create playurl", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	aInfo := assetsInfo{
		Host:    fh,
		PlayURL: playURL,
		Assets:  make([]*assetInfo, 0, len(assets)),
	}
	for _, asset := range assets {
		mpds := make([]mpdInfo, 0, len(asset.MPDs))
		for _, mpd := range asset.MPDs {
			mpds = append(mpds, mpdInfo{
				Path: mpd.Name,
				Desc: mpd.Title,
				Dur:  mpd.Dur,
			})
		}
		sort.Slice(mpds, func(i, j int) bool {
			return mpds[i].Path < mpds[j].Path
		})
		assetInfo := assetInfo{
			Path:      asset.AssetPath,
			LoopDurMS: asset.LoopDurMS,
			MPDs:      mpds,
		}
		aInfo.Assets = append(aInfo.Assets, &assetInfo)
	}
	w.Header().Set("Content-Type", "text/html")

	templateName := "urlgen.html"
	var data urlGenData
	switch r.URL.Path {
	case "/urlgen/mpds":
		asset := r.URL.Query().Get("asset")
		for _, a := range aInfo.Assets {
			if a.Path == asset {
				data.MPDs = mpdsFromAssetInfo(a)
				data.MPDs[0].Selected = true
			}
		}
		templateName = "mpds"
	case "/urlgen/create":
		data = createURL(r, aInfo)
	default:
		data, err = createInitData(r, aInfo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// Execute the template and handle errors
	if err := s.htmlTemplates.ExecuteTemplate(w, templateName, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func mpdsFromAssetInfo(a *assetInfo) []mpdWithSelect {
	mpds := make([]mpdWithSelect, len(a.MPDs))
	for i, mpd := range a.MPDs {
		mpds[i] = mpdWithSelect{Name: mpd.Path}
	}
	return mpds
}

const (
	defaultTimeSubsDur = "900"
	defaultTimeSubsReg = "0"
)

type urlGenData struct {
	PlayURL                     string
	URL                         string
	Host                        string
	Assets                      []assetWithSelect
	MPDs                        []mpdWithSelect
	Stl                         segmentTimelineType
	Tsbd                        int // time-shift buffer depth in seconds
	MinimumUpdatePeriodS        string
	SuggestedPresentationDelayS string
	Ato                         string // availabilityTimeOffset, floating point seconds or "inf"
	ChunkDur                    string // chunk duration (float in seconds)
	LlTarget                    int    // low-latency target (in milliseconds)
	TimeSubsStpp                string // languages for generated subtitles in stpp-format (comma-separated)
	TimeSubsWvtt                string // languages for generated subtitles in wvtt-format (comma-separated)
	TimeSubsDur                 string // cue duration of generated subtitles (in milliseconds)
	TimeSubsReg                 string // 0 for bottom and 1 for top
	UTCTiming                   string
	Periods                     string   // number of periods per hour (1-60)
	Continuous                  bool     // period continuity signaling
	StartNR                     string   // startNumber (default=0) -1 translates to no value in MPD (fallback to default = 1)
	Start                       string   // sets timeline start (and availabilityStartTime) relative to Epoch (in seconds)
	Stop                        string   // sets stop-time for time-limited event (in seconds)
	StartRel                    string   // sets timeline start (and availabilityStartTime) relative to now (in seconds). Normally negative value.
	StopRel                     string   // sets stop-time for time-limited event relative to now (in seconds)
	Scte35Var                   string   // SCTE-35 insertion variant
	StatusCodes                 string   // comma-separated list of response code patterns to return
	Traffic                     string   // comma-separated list of up/down/slow/hang intervals for one or more BaseURLs in MPD
	Errors                      []string // error messages to display due to bad configuration
}

var initData urlGenData

func init() {
	initData.Assets = []assetWithSelect{
		{AssetPath: "Choose an asset...", MPDs: []mpdWithSelect{{Name: "Choose an asset first"}}},
	}
	initData.Stl = Number
	initData.Tsbd = defaultTimeShiftBufferDepthS
	initData.LlTarget = defaultLatencyTargetMS
	initData.TimeSubsDur = defaultTimeSubsDur
	initData.TimeSubsReg = defaultTimeSubsReg
}

type assetWithSelect struct {
	AssetPath string
	Selected  bool
	MPDs      []mpdWithSelect
}

type mpdWithSelect struct {
	Name     string
	Selected bool
}

type segmentTimelineType string

const (
	Number         segmentTimelineType = "nr"
	TimelineTime   segmentTimelineType = "tlt"
	TimelineNumber segmentTimelineType = "tlnr"
)

func createInitData(r *http.Request, aInfo assetsInfo) (data urlGenData, err error) {
	data = initData
	data.Assets = make([]assetWithSelect, 0, len(aInfo.Assets)+1)
	data.MPDs = nil
	data.Assets = append(data.Assets, assetWithSelect{
		AssetPath: "Choose an asset...", Selected: true,
		MPDs: []mpdWithSelect{{Name: "Choose an asset first"}},
	})
	for i := range aInfo.Assets {
		data.Assets = append(data.Assets, assetWithSelect{AssetPath: aInfo.Assets[i].Path})
	}
	data.Host = aInfo.Host
	return data, nil
}

// createURL creates a URL from the request parameters. Errors are returned in ErrorMsg field.
func createURL(r *http.Request, aInfo assetsInfo) urlGenData {
	q := r.URL.Query()
	var sb strings.Builder // Used to build URL
	asset := q.Get("asset")
	mpd := q.Get("mpd")
	// fmt.Println("create", asset, mpd)
	data := initData
	data.Assets = make([]assetWithSelect, 0, len(aInfo.Assets))
	data.MPDs = nil
	for i := range aInfo.Assets {
		a := assetWithSelect{AssetPath: aInfo.Assets[i].Path}
		if a.AssetPath == asset {
			a.Selected = true
			data.MPDs = make([]mpdWithSelect, 0, len(a.MPDs)+1)
			for j := range aInfo.Assets[i].MPDs {
				name := aInfo.Assets[i].MPDs[j].Path
				selected := name == mpd
				data.MPDs = append(data.MPDs, mpdWithSelect{Name: name, Selected: selected})
			}
		}
		data.Assets = append(data.Assets, a)
	}
	sb.WriteString(aInfo.Host)
	sb.WriteString("/livesim2/")
	stl := segmentTimelineType(q.Get("stl"))
	switch stl {
	case Number:
		data.Stl = Number
	case TimelineTime:
		data.Stl = TimelineTime
		sb.WriteString("segtimeline_1/")
	case TimelineNumber:
		data.Stl = TimelineNumber
		sb.WriteString("segtimelinenr_1/")
	default:
		fmt.Printf("Bad stl: %s\n", stl)
	}
	tsbd := q.Get("tsbd")
	if tsbd != "" {
		t, err := strconv.Atoi(tsbd)
		if err != nil {
			panic("bad tsbd")
		}
		if t != defaultTimeShiftBufferDepthS {
			data.Tsbd = t
			sb.WriteString(fmt.Sprintf("tsbd_%d/", t))
		}
	}
	ato := q.Get("ato")
	if ato != "" {
		data.Ato = ato
		sb.WriteString(fmt.Sprintf("ato_%s/", ato))
	}
	mup := q.Get("mup")
	if mup != "" {
		data.MinimumUpdatePeriodS = mup
		sb.WriteString(fmt.Sprintf("mup_%s/", mup))
	}
	spd := q.Get("spd")
	if spd != "" {
		data.SuggestedPresentationDelayS = spd
		sb.WriteString(fmt.Sprintf("spd_%s/", spd))
	}
	startNR := q.Get("snr")
	if startNR != "" {
		data.StartNR = startNR
		sb.WriteString(fmt.Sprintf("snr_%s/", startNR))
	}
	utc := q.Get("utc")
	if utc != "" {
		data.UTCTiming = utc
		sb.WriteString(fmt.Sprintf("utc_%s/", utc))
	}
	periods := q.Get("periods")
	if periods != "" {
		data.Periods = periods
		sb.WriteString(fmt.Sprintf("periods_%s/", periods))
	}
	continuous := q.Get("continuous")
	if continuous != "" {
		data.Continuous = true
		sb.WriteString("continuous_1/")
	}
	chunkDur := q.Get("chunkdur")
	if chunkDur != "" {
		data.ChunkDur = chunkDur
		sb.WriteString(fmt.Sprintf("chunkdur_%s/", chunkDur))
	}
	if llTarget := q.Get("ltgt"); llTarget != "" {
		lt, err := strconv.Atoi(llTarget)
		if err != nil {
			panic("bad ltgt")
		}
		if lt != defaultLatencyTargetMS {
			data.LlTarget = lt
			sb.WriteString(fmt.Sprintf("ltgt_%d/", lt))
		}
	}
	start := q.Get("start")
	if start != "" {
		data.Start = start
		sb.WriteString(fmt.Sprintf("start_%s/", start))
	}
	stop := q.Get("stop")
	if stop != "" {
		data.Stop = stop
		sb.WriteString(fmt.Sprintf("stop_%s/", stop))
	}
	startRel := q.Get("startrel")
	if startRel != "" {
		data.StartRel = startRel
		sb.WriteString(fmt.Sprintf("startrel_%s/", startRel))
	}
	stopRel := q.Get("stoprel")
	if stopRel != "" {
		data.StopRel = stopRel
		sb.WriteString(fmt.Sprintf("stoprel_%s/", stopRel))
	}
	timeSubsStpp := q.Get("timesubsstpp")
	if timeSubsStpp != "" {
		data.TimeSubsStpp = timeSubsStpp
		sb.WriteString(fmt.Sprintf("timesubsstpp_%s/", timeSubsStpp))
	}
	timeSubsWvtt := q.Get("timesubswvtt")
	if timeSubsWvtt != "" {
		data.TimeSubsWvtt = timeSubsWvtt
		sb.WriteString(fmt.Sprintf("timesubswvtt_%s/", timeSubsWvtt))
	}
	timeSubsDur := q.Get("timesubsdur")
	if timeSubsDur != "" && timeSubsDur != defaultTimeSubsDur {
		data.TimeSubsDur = timeSubsDur
		sb.WriteString(fmt.Sprintf("timesubsdur_%s/", timeSubsDur))
	}
	timeSubsReg := q.Get("timesubsreg")
	if timeSubsReg != "" && timeSubsReg != defaultTimeSubsReg {
		data.TimeSubsReg = timeSubsReg
		sb.WriteString(fmt.Sprintf("timesubsreg_%s/", timeSubsReg))
	}
	scte35 := q.Get("scte35")
	if scte35 != "" {
		data.Scte35Var = scte35
		sb.WriteString(fmt.Sprintf("scte35_%s/", scte35))
	}
	statusCodes := q.Get("statuscode")
	if statusCodes != "" {
		sc := newStringConverter()
		_ = sc.ParseSegStatusCodes("statuscode", statusCodes)
		if sc.err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("bad statuscode patterns: %s", sc.err.Error()))
		}
		data.StatusCodes = statusCodes
		sb.WriteString(fmt.Sprintf("statuscode_%s/", statusCodes))
	}
	traffic := q.Get("traffic")
	if traffic != "" {
		_, err := CreateAllLossItvls(traffic)
		if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("bad traffic pattern: %s", err.Error()))
		}
		data.Traffic = traffic
		sb.WriteString(fmt.Sprintf("traffic_%s/", traffic))
	}
	sb.WriteString(fmt.Sprintf("%s/%s", asset, mpd))
	if len(data.Errors) > 0 {
		data.URL = ""
		data.PlayURL = ""
	} else {
		data.URL = sb.String()
		data.PlayURL = aInfo.PlayURL
	}
	data.Host = aInfo.Host
	return data
}
