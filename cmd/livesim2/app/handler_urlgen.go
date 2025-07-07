// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/drm"
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
			Path:         asset.AssetPath,
			LoopDurMS:    asset.LoopDurMS,
			MPDs:         mpds,
			PreEncrypted: asset.refRep.PreEncrypted,
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
	case "/urlgen/drms":
		asset := r.URL.Query().Get("asset")
		for _, aI := range aInfo.Assets {
			if aI.Path == asset {
				data.DRMs = drmsFromAssetInfo(aI, s.Cfg.DrmCfg, "")
				data.DRMs[0].Selected = true
			}
		}
		templateName = "drms"
	case "/urlgen/create":
		data = createURL(r, aInfo, s.Cfg.DrmCfg)
	default:
		data, err = s.createInitData(aInfo)
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

func mpdsFromAssetInfo(a *assetInfo) []nameWithSelect {
	mpds := make([]nameWithSelect, len(a.MPDs))
	for i, mpd := range a.MPDs {
		mpds[i] = nameWithSelect{Name: mpd.Path}
	}
	return mpds
}

func drmsFromAssetInfo(a *assetInfo, drmCfg *drm.DrmConfig, selected string) []nameWithSelect {
	if drmCfg == nil {
		return nil
	}
	drmPkgs := drmCfg.Packages
	if a != nil && a.PreEncrypted {
		return []nameWithSelect{{Name: "None", Selected: true,
			Desc: fmt.Sprintf("No DRM choice available because asset %q is pre-encrypted", a.Path)}}
	}
	drms := make([]nameWithSelect, 0, 3+len(drmPkgs))
	drms = append(drms, nameWithSelect{Name: "None", Desc: "No DRM", Selected: selected == ""})
	drms = append(drms, nameWithSelect{Name: "eccp-cbcs", Desc: "ECCP with cbcs encryption", Selected: selected == "eccp-cbcs"})
	drms = append(drms, nameWithSelect{Name: "eccp-cenc", Desc: "ECCP with cenc encryption", Selected: selected == "eccp-cenc"})
	for _, pkg := range drmPkgs {
		drms = append(drms, nameWithSelect{Name: pkg.Name, Desc: pkg.Desc, Selected: selected == pkg.Name})
	}
	return drms
}

const (
	defaultTimeSubsDur = "900"
	defaultTimeSubsReg = "0"
)

//nolint:lll
type urlGenData struct {
	PlayURL                     string
	URL                         string
	Host                        string
	Assets                      []assetWithSelect
	MPDs                        []nameWithSelect
	DRMs                        []nameWithSelect
	Stl                         segmentTimelineType
	Tsbd                        int // time-shift buffer depth in seconds
	MinimumUpdatePeriodS        string
	SuggestedPresentationDelayS string
	Ato                         string // availabilityTimeOffset, floating point seconds or "inf"
	ChunkDur                    string // chunk duration (float in seconds)
	LlTarget                    int    // low-latency target (in milliseconds)
	LowDelay                    bool   // low-latency mode enabled
	TimeSubsStpp                string // languages for generated subtitles in stpp-format (comma-separated)
	TimeSubsWvtt                string // languages for generated subtitles in wvtt-format (comma-separated)
	TimeSubsDur                 string // cue duration of generated subtitles (in milliseconds)
	TimeSubsReg                 string // 0 for bottom and 1 for top
	Drm                         string // empty means no DRM setup
	UTCTiming                   string
	Periods                     string   // number of periods per hour (1-60)
	Continuous                  bool     // period continuity signaling
	StartNR                     string   // startNumber (default=0) -1 translates to no value in MPD (fallback to default = 1)
	Start                       string   // sets timeline start (and availabilityStartTime) relative to Epoch (in seconds)
	Stop                        string   // sets stop-time for time-limited event (in seconds)
	StartRel                    string   // sets timeline start (and availabilityStartTime) relative to now (in seconds). Normally negative value.
	StopRel                     string   // sets stop-time for time-limited event relative to now (in seconds)
	Scte35Var                   string   // SCTE-35 insertion variant
	PatchTTL                    string   // MPD Patch TTL  inv value in seconds (> 0 to be valid))
	StatusCodes                 string   // comma-separated list of response code patterns to return
	AnnexI                      string   // comma-separated list of Annex I parameters as key=value pairs
	Traffic                     string   // comma-separated list of up/down/slow/hang intervals for one or more BaseURLs in MPD
	Errors                      []string // error messages to display due to bad configuration
}

var initData urlGenData

func init() {
	initData.Assets = []assetWithSelect{
		{AssetPath: "Choose an asset...", MPDs: []nameWithSelect{{Name: "Choose an asset first"}}},
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
	MPDs      []nameWithSelect
}

type nameWithSelect struct {
	Name     string
	Desc     string
	Selected bool
}

type segmentTimelineType string

const (
	Number         segmentTimelineType = "nr"
	TimelineTime   segmentTimelineType = "tlt"
	TimelineNumber segmentTimelineType = "tlnr"
)

func (s *Server) createInitData(aInfo assetsInfo) (data urlGenData, err error) {
	data = initData
	data.Assets = make([]assetWithSelect, 0, len(aInfo.Assets)+1)
	data.MPDs = nil
	data.Assets = append(data.Assets, assetWithSelect{
		AssetPath: "Choose an asset...", Selected: true,
		MPDs: []nameWithSelect{{Name: "Choose an asset first"}},
	})
	for i := range aInfo.Assets {
		data.Assets = append(data.Assets, assetWithSelect{AssetPath: aInfo.Assets[i].Path})
	}
	data.Host = aInfo.Host
	data.DRMs = drmsFromAssetInfo(nil, s.Cfg.DrmCfg, "")
	data.LowDelay = false
	return data, nil
}

// createURL creates a URL from the request parameters. Errors are returned in ErrorMsg field.
func createURL(r *http.Request, aInfo assetsInfo, drmCfg *drm.DrmConfig) urlGenData {
	q := r.URL.Query()
	var sb strings.Builder // Used to build URL
	asset := q.Get("asset")
	mpd := q.Get("mpd")
	// fmt.Println("create", asset, mpd)
	data := initData
	data.Assets = make([]assetWithSelect, 0, len(aInfo.Assets))
	data.MPDs = nil
	var aI *assetInfo
	for i := range aInfo.Assets {
		a := assetWithSelect{AssetPath: aInfo.Assets[i].Path}
		if a.AssetPath == asset {
			a.Selected = true
			aI = aInfo.Assets[i]
			data.MPDs = make([]nameWithSelect, 0, len(a.MPDs)+1)
			for j := range aInfo.Assets[i].MPDs {
				name := aInfo.Assets[i].MPDs[j].Path
				selected := name == mpd
				data.MPDs = append(data.MPDs, nameWithSelect{Name: name, Selected: selected})
			}
		}
		data.Assets = append(data.Assets, a)
	}
	sb.WriteString(aInfo.Host)
	sb.WriteString("/livesim2/")
	if q.Get("lowdelay") != "" {
		data.LowDelay = true
	}
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
	if ptl := q.Get("patch-ttl"); ptl != "" {
		patchTTL, err := strconv.Atoi(ptl)
		if err != nil {
			panic("bad patch-ttl")
		}
		if patchTTL > 0 {
			data.PatchTTL = ptl
			sb.WriteString(fmt.Sprintf("patch_%s/", ptl))
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
	drm := q.Get("drm")
	switch drm {
	case "", "None":
		data.Drm = drm
	default:
		sb.WriteString(fmt.Sprintf("drm_%s/", drm))
	}
	data.DRMs = drmsFromAssetInfo(aI, drmCfg, q.Get("drm"))
	scte35 := q.Get("scte35")
	if scte35 != "" {
		data.Scte35Var = scte35
		sb.WriteString(fmt.Sprintf("scte35_%s/", scte35))
	}
	annexI := q.Get("annexI")
	if annexI != "" {
		data.AnnexI = annexI
		sb.WriteString(fmt.Sprintf("annexI_%s/", annexI))
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

	if data.LowDelay {
		sb.WriteString(fmt.Sprintf("%s/%s?lowdelay=1", asset, mpd))
	} else {
		sb.WriteString(fmt.Sprintf("%s/%s", asset, mpd))
	}
	
	if annexI != "" {
		query, err := queryFromAnnexI(annexI)
		if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("bad annexI: %s", err.Error()))
		}
		if strings.Contains(sb.String(), "?") {
			sb.WriteString(strings.Replace(query, "?", "&", 1))
		} else {
			sb.WriteString(query)
		}
	}
	if len(data.Errors) > 0 {
		data.URL = ""
		data.PlayURL = ""
	} else {
		data.URL = sb.String()
		data.PlayURL = fmt.Sprintf(aInfo.PlayURL, url.QueryEscape(data.URL))
	}
	data.Host = aInfo.Host
	return data
}

func queryFromAnnexI(annexI string) (string, error) {
	out := ""
	pairs := strings.Split(annexI, ",")
	for i, p := range pairs {
		parts := strings.Split(p, "=")
		if len(parts) != 2 {
			return "", fmt.Errorf("bad key-value pair: %s", p)
		}
		if i == 0 {
			out += fmt.Sprintf("?%s=%s", parts[0], parts[1])
		} else {
			out += fmt.Sprintf("&%s=%s", parts[0], parts[1])
		}
	}
	return out, nil
}
