package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/cmaf"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

type ingesterState int

const CMAFIngestVersion = "1.1"

const (
	ingesterStateNotStarted ingesterState = iota
	ingesterStateRunning
	ingesterStateStopped
)

type cmafIngesterMgr struct {
	nr        atomic.Uint64
	ingesters map[uint64]*cmafIngester
	state     ingesterState
	s         *Server
	cancels   map[uint64]context.CancelFunc
}

type cmafIngester struct {
	mgr            *cmafIngesterMgr
	user           string
	passWord       string
	destRoot       string
	destName       string
	url            string
	log            *slog.Logger
	testNowMS      *int
	dur            *int
	nrSegsToSend   *int // calculate from dur and segDur
	streamsURLs    bool
	useChunked     bool
	cfg            *ResponseConfig
	asset          *asset
	repsData       []cmafRepData
	nextSegTrigger chan struct{}
	state          ingesterState
	report         []string
}

func NewCmafIngesterMgr(s *Server) *cmafIngesterMgr {
	return &cmafIngesterMgr{
		ingesters: make(map[uint64]*cmafIngester),
		cancels:   make(map[uint64]context.CancelFunc),
		state:     ingesterStateNotStarted,
		s:         s,
	}
}

func (cm *cmafIngesterMgr) Start() {
	cm.state = ingesterStateRunning
}

func (cm *cmafIngesterMgr) Close() {
	for i, cancel := range cm.cancels {
		if cm.ingesters[i].state == ingesterStateRunning {
			cancel()
		}
	}
}

func (cm *cmafIngesterMgr) NewCmafIngester(req CmafIngesterSetup) (nr uint64, err error) {
	if cm.state != ingesterStateRunning {
		return 0, fmt.Errorf("CMAF ingester manager not running")
	}
	for { // Get unique atomic number
		prev := cm.nr.Load()
		nr = prev + 1
		if cm.nr.CompareAndSwap(prev, nr) {
			break
		}
	}

	log := slog.Default().With(slog.Uint64("ingester", nr))

	mpdReq := httptest.NewRequest("GET", req.URL, nil)
	if req.TestNowMS != nil {
		mpdReq.URL.RawQuery = fmt.Sprintf("nowMS=%d", *req.TestNowMS)
	}
	nowMS, cfg, errHT := cfgFromRequest(mpdReq, log)
	if errHT != nil {
		return 0, fmt.Errorf("failed to get config from request: %w", errHT)
	}

	contentPart := cfg.URLContentPart()
	log.Debug("CMAF ingest content", "url", contentPart)
	asset, ok := cm.s.assetMgr.findAsset(contentPart)
	if !ok {
		return 0, fmt.Errorf("unknown asset %q", contentPart)
	}
	_, mpdName := path.Split(contentPart)
	liveMPD, err := LiveMPD(asset, mpdName, cfg, nil, nowMS)
	if err != nil {
		return 0, fmt.Errorf("failed to generate live MPD: %w", err)
	}

	// Extract list of all representations with their information

	period := liveMPD.Periods[0]
	nrReps := 0
	for _, a := range period.AdaptationSets {
		nrReps += len(a.Representations)
	}

	repsData := make([]cmafRepData, 0, nrReps)

	adaptationSets := orderAdaptationSetsByContentType(period.AdaptationSets)

	for _, a := range adaptationSets {
		contentType := a.ContentType
		var mimeType string
		switch contentType {
		case "video":
			mimeType = "video/mp4"
		case "audio":
			mimeType = "audio/mp4"
		case "text":
			mimeType = "application/mp4"
		default:
			return 0, fmt.Errorf("unknown content type: %s", contentType)
		}
		for _, r := range a.Representations {
			segTmpl := r.GetSegmentTemplate()
			ext, err := cmaf.CMAFExtensionFromContentType(string(contentType))
			if err != nil {
				return 0, fmt.Errorf("error getting CMAF extension: %w", err)
			}
			rd := cmafRepData{
				repID:        r.Id,
				contentType:  string(contentType),
				mimeType:     mimeType,
				initPath:     replaceIdentifiers(r, segTmpl.Initialization),
				extension:    ext,
				mediaPattern: replaceIdentifiers(r, segTmpl.Media),
				bandWidth:    r.Bandwidth,
				roles:        r.Parent().Roles,
			}
			repsData = append(repsData, rd)
		}
	}

	c := cmafIngester{
		mgr:            cm,
		user:           req.User,
		passWord:       req.PassWord,
		destRoot:       req.DestRoot,
		destName:       req.DestName,
		url:            req.URL,
		testNowMS:      req.TestNowMS,
		dur:            req.Duration,
		streamsURLs:    req.StreamsURLs,
		useChunked:     cfg.ChunkDurS != nil,
		log:            log,
		cfg:            cfg,
		asset:          asset,
		repsData:       repsData,
		state:          ingesterStateNotStarted,
		nextSegTrigger: make(chan struct{}),
	}
	if c.dur != nil {
		c.nrSegsToSend = m.Ptr(*c.dur * 1000 / asset.SegmentDurMS)
	}
	cm.ingesters[nr] = &c

	return nr, nil
}

func (cm *cmafIngesterMgr) startIngester(nr uint64) {
	c, ok := cm.ingesters[nr]
	if !ok {
		return
	}
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	cm.cancels[nr] = cancel
	go c.start(ctx)
}

type cmafRepData struct {
	repID        string
	contentType  string
	mimeType     string
	initPath     string
	mediaPattern string
	extension    string
	bandWidth    uint32
	roles        []*m.DescriptorType
}

// start starts the main ingest loop for sending init and media packets.
// It calculates the availability time for the next segment and
// then waits until that time to send all segments.
// Segments are sent in parallel for all representation, as
// low-latency mode requires parallel chunked-transfer encoding streams.
// If TestNowMS is set, the ingester runs in test mode, and
// the time is not taken from the system clock, but start from
// TestNowMS and increments to next segment every time testNextSegment()
// is called.
func (c *cmafIngester) start(ctx context.Context) {

	defer func() {
		c.state = ingesterStateStopped
	}()

	// Finally we should send off the init segments
	// and then start the loop for sending the media segments

	var initBin []byte
	contentType := "application/mp4"
	startTimeS := int64(c.cfg.StartTimeS)
	nrInitErrors := 0
	for _, rd := range c.repsData {
		prefix, lang, ok, err := matchTimeSubsInitLang(c.cfg, rd.initPath)
		if ok {
			if err != nil {
				msg := fmt.Sprintf("error matching time subs init lang: %v", err)
				c.report = append(c.report, msg)
				c.log.Error(msg)
				return
			}
			init := createTimeSubsInitSegment(prefix, lang, SUBS_TIME_TIMESCALE)
			setInitProps(init, rd, startTimeS)
			sw := bits.NewFixedSliceWriter(int(init.Size()))
			err := init.EncodeSW(sw)
			if err != nil {
				msg := fmt.Sprintf("Error encoding init segment: %v", err)
				c.report = append(c.report, msg)
				c.log.Error(msg)
				return
			}
			initBin = sw.Bytes()
		} else {
			match, err := matchInit(rd.initPath, c.cfg, c.mgr.s.Cfg.DrmCfg, c.asset)
			if err != nil {
				msg := fmt.Sprintf("Error matching init segment: %v", err)
				c.report = append(c.report, msg)
				c.log.Error(msg)
			}
			if !match.isInit {
				msg := fmt.Sprintf("Error matching init segment: %v", err)
				c.report = append(c.report, msg)
				c.log.Error(msg)
			}
			contentType = match.rep.SegmentType()
			initBin, err = setRawInitProps(match.init, rd, startTimeS)
			if err != nil {
				msg := fmt.Sprintf("Error setting init times: %v", err)
				c.report = append(c.report, msg)
				c.log.Error(msg)
			}
		}

		err = c.sendInitSegment(ctx, rd, initBin)
		if err != nil {
			msg := fmt.Sprintf("error uploading init segment: %v", err)
			c.report = append(c.report, msg)
			c.log.Error(msg)
			nrInitErrors++
		} else {
			c.log.Info("Sent init segment", "path", rd.initPath, "contentType", contentType, "size", len(initBin))
			c.report = append(c.report, fmt.Sprintf("Sent init segment %s", rd.initPath))
		}
	}
	if nrInitErrors > 0 {
		msg := fmt.Sprintf("Number of init errors: %d", nrInitErrors)
		c.report = append(c.report, msg)
		c.log.Error("could not upload init segments", "nrErrors", nrInitErrors)
		return
	}

	// Now calculate the availability time for the next segment
	var nowMS int
	if c.testNowMS != nil {
		nowMS = *c.testNowMS
	} else {
		nowMS = int(time.Now().UnixNano() / 1e6)
	}
	c.state = ingesterStateRunning

	refRep := c.asset.refRep
	lastNr := findLastSegNr(c.cfg, c.asset, nowMS, refRep)
	nextSegNr := lastNr + 1
	lastSegNrToSend := -1

	if c.nrSegsToSend != nil {
		lastSegNrToSend = nextSegNr + *c.nrSegsToSend
	}
	if lastSegNrToSend > 0 {
		c.log.Debug("First and last segment number to send", "first", nextSegNr, "last", lastSegNrToSend)
	}

	availabilityTime, err := calcSegmentAvailabilityTime(c.asset, refRep, uint32(nextSegNr), c.cfg)
	if err != nil {
		msg := fmt.Sprintf("Error calculating segment availability time: %v", err)
		c.report = append(c.report, msg)
		c.log.Error(msg)
		return
	}
	c.log.Info("Next segment availability time", "time", availabilityTime)
	var timer *time.Timer
	deltaTime := 24 * time.Hour
	if c.testNowMS == nil {
		deltaTime = time.Duration(availabilityTime-int64(nowMS)) * time.Millisecond
	}
	timer = time.NewTimer(deltaTime)
	defer func() {
		if !timer.Stop() {
			<-timer.C
		}
	}()

	// Main loop for sending segments
	for {
		if lastSegNrToSend >= 0 && nextSegNr > lastSegNrToSend {
			c.log.Info("Last segment sent", "nr", lastSegNrToSend)
			return
		}
		c.log.Info("Waiting for next segment")
		select {
		case <-timer.C:
			// Send next segment
		case <-c.nextSegTrigger:
			// Send next segment
		case <-ctx.Done():
			c.log.Info("Context done, stopping ingest")
			return
		}
		isLast := nextSegNr == lastSegNrToSend
		err := c.sendMediaSegments(ctx, nextSegNr, int(availabilityTime), isLast)
		if err != nil {
			msg := fmt.Sprintf("Error sending media segments: %v", err)
			c.report = append(c.report, msg)
			c.log.Error(msg)
			return
		}
		nextSegNr++
		availabilityTime, err = calcSegmentAvailabilityTime(c.asset, refRep, uint32(nextSegNr), c.cfg)
		if err != nil {
			msg := fmt.Sprintf("Error calculating segment availability time: %v", err)
			c.report = append(c.report, msg)
			c.log.Error(msg)
			return
		}
		if c.testNowMS == nil {
			nowMS = int(time.Now().UnixNano() / 1e6)
		}

		c.log.Info("Next segment availability time", "time", availabilityTime)
		if c.testNowMS == nil {
			deltaTime := time.Duration(availabilityTime-int64(nowMS)) * time.Millisecond
			for deltaTime <= 0 {
				msg := fmt.Sprintf("Segment availability time in the past: %d", availabilityTime)
				c.report = append(c.report, msg)
				c.log.Error(msg)
				err := c.sendMediaSegments(ctx, nextSegNr, int(availabilityTime), false /* isLast */)
				if err != nil {
					msg := fmt.Sprintf("Error sending media segments: %v", err)
					c.report = append(c.report, msg)
					c.log.Error(msg)
					return
				}
				nextSegNr++
				availabilityTime, err = calcSegmentAvailabilityTime(c.asset, refRep, uint32(nextSegNr), c.cfg)
				if err != nil {
					msg := fmt.Sprintf("Error calculating segment availability time: %v", err)
					c.report = append(c.report, msg)
					c.log.Error(msg)
					return
				}
				nowMS = int(time.Now().UnixNano() / 1e6)
				deltaTime = time.Duration(availabilityTime-int64(nowMS)) * time.Millisecond
			}
			timer.Reset(deltaTime)
		}
	}

	// connect to URL
	// if user != "", do basic authentication
	// Use URL to get an MPD from internal engine
	// Generate init segments as described in MPD
	// Do HTTP PUT for each init segment
	// Then calculate next segment number and pause/sleep until time to send it.
	// Loop:
	//    Calculate time for next segment, and set timer
	//    At timer, push all generated segments (all representations)
	//    Count how many segments have been pushed, and stop
	//    if limit is passed.
	//    Note, for low-latency, one needs parallel HTTP sessions
	//    in H1/H2. There therefore need to be as many HTTP sessions
	//    to the same host as there are representations pushed.
	//
	// Error handling:
	//    If getting behind in time or not successful
	//        gather statistics into own report
	//    The upload client (HTTP client) should have timeout.
	// Stopping:
	// There should be a context so that one can cancel this loop
	//    * Either triggered by shutting down the server, or by REST DELETE
	//    * If DELETE, one should get a report back
	//    * Any ongoing uploads should ideally finish before stopping
	//      so that all representations are synchronized and have the same
	//      number of segments
	//
	// Reporting:
	//    * It should be possible to ask for a report by sending a GET request
	//    * DELETE should also return a report of what has been sent
	//
	// CMAF-Ingest interface
	//
	//    * Interface #1 may be to only send segments
	//    * The metadata then need to be added like role in `kind` boxes`, but also prft
	//    * Sending an MPD would help
	//
	//    * SCTE-35 events should be sent as a separate event stream. This will mostly have
	//      empty segments. Should check what AWS is outputting to get a reference

	//
}

func (c *cmafIngester) triggerNextSegment() {
	c.nextSegTrigger <- struct{}{}
}

func (c *cmafIngester) dest() string {
	d := c.destRoot
	if c.destName != "" {
		d = fmt.Sprintf("%s/%s", c.destRoot, c.destName)
	}
	return d
}

func (c *cmafIngester) sendInitSegment(ctx context.Context, rd cmafRepData, rawInitSeg []byte) error {
	fileName := fmt.Sprintf("%s/init%s", rd.repID, rd.extension)
	if c.streamsURLs {
		fileName = fmt.Sprintf("Streams(%s%s)", rd.repID, rd.extension)
	}
	url := fmt.Sprintf("%s/%s", c.dest(), fileName)
	buf := bytes.NewBuffer(rawInitSeg)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, buf)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	slog.Info("Sending init segment", "fileName", fileName, "url", url)
	setReqHeaders(req, rd.contentType, c.user, c.passWord)
	err = c.sendRequest(ctx, req, url)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	return nil
}

func (c *cmafIngester) sendRequest(ctx context.Context, req *http.Request, url string) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Error sending request: %w", err)
	}
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Error reading response body: %w", err)
	}
	if resp.StatusCode >= 300 {
		slog.Warn("Bad status code", "code", resp.StatusCode, "url", url)
		return fmt.Errorf("bad status code: %d", resp.StatusCode)
	}
	defer func() {
		resp.Body.Close()
	}()
	return nil
}

func setReqHeaders(req *http.Request, contentType, user, password string) {
	req.Header.Set("Connection", "keep-alive")
	if user != "" && password != "" {
		req.SetBasicAuth(user, password)
	}
	switch contentType {
	case "video":
		req.Header.Set("Content-Type", "video/mp4")
	case "audio":
		req.Header.Set("Content-Type", "audio/mp4")
	case "text":
		req.Header.Set("Content-Type", "application/mp4")
	default:
		slog.Warn("unknown content type", "type", contentType)
	}
	req.Header.Set("DASH-IF-Ingest", CMAFIngestVersion)
	req.Header.Set("DASH-IF-livesim2", internal.GetVersion())
}

// sendMediaSegments sends all media segments for all representations. isLast triggers lmsg insertion.
func (c *cmafIngester) sendMediaSegments(ctx context.Context, nextSegNr, nowMS int, isLast bool) error {
	c.log.Debug("Start media segment", "nr", nextSegNr, "nowMS", nowMS, "useChunked", c.useChunked)
	wTimes := calcWrapTimes(c.asset, c.cfg, nowMS+50, m.Duration(100*time.Millisecond))
	wg := sync.WaitGroup{}
	if c.cfg.SegTimelineFlag {
		var segPart string
		var refSegEntries segEntries
		atoMS := int(c.cfg.getAvailabilityTimeOffsetS() * 1000)
		for idx, rd := range c.repsData {
			var se segEntries
			// The first representation is used as reference for generating timeline entries
			if idx == 0 {
				refSegEntries = c.asset.generateTimelineEntries(rd.repID, wTimes, atoMS, nil)
				se = refSegEntries
			} else {
				switch rd.contentType {
				case "video", "text", "image":
					se = c.asset.generateTimelineEntries(rd.repID, wTimes, atoMS, nil)
				case "audio":
					se = c.asset.generateTimelineEntriesFromRef(refSegEntries, rd.repID)
				default:
					return fmt.Errorf("unknown content type %s", rd.contentType)
				}
			}
			segTime := int(se.lastTime())
			segPart = replaceTimeOrNr(rd.mediaPattern, segTime)
			segPath := fmt.Sprintf("%s/%d%s", rd.repID, segTime, rd.extension)
			if c.streamsURLs {
				segPath = fmt.Sprintf("Streams(%s%s)", rd.repID, rd.extension)
			}
			wg.Add(1)
			go c.sendMediaSegment(ctx, &wg, segPath, segPart, rd.contentType, nextSegNr, nowMS, isLast)
		}
	} else {
		for _, rd := range c.repsData {
			segPart := replaceTimeOrNr(rd.mediaPattern, nextSegNr)
			segPath := fmt.Sprintf("%s/%d%s", rd.repID, nextSegNr, rd.extension)
			if c.streamsURLs {
				segPath = fmt.Sprintf("Streams(%s%s)", rd.repID, rd.extension)
			}
			wg.Add(1)
			go c.sendMediaSegment(ctx, &wg, segPath, segPart, rd.contentType, nextSegNr, nowMS, isLast)
		}
	}
	wg.Wait()
	return nil
}

// sendMediaSegment sends a media segment to the destination URL.
// The segment may be written in chunks, rather than as a whole.
func (c *cmafIngester) sendMediaSegment(ctx context.Context, wg *sync.WaitGroup, segPath, segPart, contentType string,
	segNr, nowMS int, isLast bool) {
	defer wg.Done()

	u := fmt.Sprintf("%s/%s", c.dest(), segPath)
	c.log.Info("send media segment", "path", segPath, "segNr", segNr, "nowMS", nowMS, "url", u, "chunked", c.useChunked)

	nrBytesCh := make(chan int)
	defer close(nrBytesCh)
	writeMoreCh := make(chan struct{})
	defer close(writeMoreCh)
	finishedSendCh := make(chan struct{})
	defer close(finishedSendCh)

	src := newCmafSource(nrBytesCh, writeMoreCh, c.log, u, contentType, c.user, c.passWord, c.useChunked)

	// Create media segment based on number and send it to segPath
	if c.useChunked {
		go src.startReadAndSendChunked(ctx, finishedSendCh)
	}
	code, err := writeSegment(ctx, src, c.log, c.cfg, c.mgr.s.Cfg.DrmCfg, c.mgr.s.assetMgr.vodFS,
		c.asset, segPart, nowMS, c.mgr.s.textTemplates, isLast)
	c.log.Info("writeSegment", "code", code, "err", err)
	if err != nil {
		c.log.Error("writeSegment", "code", code, "err", err)
		var tooEarly errTooEarly
		switch {
		case errors.Is(err, errNotFound):
			c.log.Error("segment not found", "path", segPath)
			return
		case errors.As(err, &tooEarly):
			c.log.Error("segment too early", "path", segPath)
			return
		case errors.Is(err, errGone):
			c.log.Error("segment gone", "path", segPath)
			return
		default:
			c.log.Error("writeSegment", "err", err)
			http.Error(src, "writeSegment", http.StatusInternalServerError)
			return
		}
	}
	if c.useChunked {
		<-writeMoreCh   // Capture final message
		nrBytesCh <- -1 // Signal that we are done to Read (that reads and pushes to remote)
		<-finishedSendCh
	} else {
		// Write should have written everything to a c.buffer
		req, err := http.NewRequestWithContext(ctx, "PUT", u, src.buffer)
		if err != nil {
			c.log.Error("creating request", "err", err)
			http.Error(src, "writeSegment", http.StatusInternalServerError)
			return
		}
		setReqHeaders(req, src.contentType, src.user, src.password)
		slog.Info("Sending media segment", "url", u)
		err = c.sendRequest(ctx, req, u)
		if err != nil {
			c.log.Error("sending request", "err", err)
			http.Error(src, "writeSegment", http.StatusInternalServerError)
			return
		}
	}
}

// cmafSource intermediates HTTP response writer and client push writer
// It provides a Read method that the client can use to read the data.
// If useChunked, the data is sent in chunks, otherwise as a whole using Content-Length.
type cmafSource struct {
	ctx         context.Context
	req         *http.Request
	contentType string
	nrBytesCh   chan int // Used to signal how many bytes have been written to local buffer.
	writeMoreCh chan struct{}
	url         string
	h           http.Header
	status      int
	log         *slog.Logger
	buf         []byte
	buffer      *bytes.Buffer
	bufLevel    int // Keeping track of local buffer
	offset      int // Offset in local buffer
	user        string
	password    string
	useChunked  bool
}

func newCmafSource(nrBytesCh chan int, writeMoreCh chan struct{}, log *slog.Logger, url string, contentType, user, password string,
	useChunked bool) *cmafSource {
	cs := cmafSource{
		url:         url,
		contentType: contentType,
		h:           make(http.Header),
		log:         log,
		nrBytesCh:   nrBytesCh,
		writeMoreCh: writeMoreCh,
		user:        user,
		password:    password,
		useChunked:  useChunked,
	}
	if useChunked {
		cs.buf = make([]byte, 64*1024)
	}
	return &cs
}

func (cs *cmafSource) startReadAndSendChunked(ctx context.Context, finishedCh chan struct{}) {
	cs.writeMoreCh <- struct{}{} // Get the writer going
	cs.ctx = ctx
	req, err := http.NewRequestWithContext(ctx, "PUT", cs.url, cs)
	if err != nil {
		cs.log.Error("creating request", "err", err)
		return
	}
	setReqHeaders(req, cs.contentType, cs.user, cs.password)
	cs.req = req
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cs.log.Error("creating request", "err", err)
		return
	}
	if resp.StatusCode >= 300 {
		cs.log.Warn("Bad status code", "code", resp.StatusCode)
		return
	}
	_, err = io.ReadAll(resp.Body) // Normally no body, but ready to be sure that buffers are cleared
	if err != nil {
		cs.log.Warn("Error reading response body", "err", err)
	}
	defer func() {
		cs.log.Debug("Closing body", "url", cs.url)
		resp.Body.Close()
	}()
	finishedCh <- struct{}{}
}

func (cs *cmafSource) Header() http.Header {
	return cs.h
}

func (cs *cmafSource) Flush() {
	cs.log.Debug("Flush")
}

func (cs *cmafSource) Write(b []byte) (int, error) {
	if !cs.useChunked {
		contentLength := -1 // Set to -1 to signal that we have checked once
		if cl, ok := cs.h["Content-Length"]; ok {
			cl, err := strconv.Atoi(cl[0])
			if err != nil {
				cs.log.Error("Content-Length", "err", err)
			}
			contentLength = cl
		}
		if contentLength <= 0 {
			return 0, fmt.Errorf("bad content length: %d", contentLength)
		}
		if len(b) != contentLength {
			cs.log.Warn("Content-Length mismatch", "length", len(b), "contentLength", contentLength)
		}
		if cs.buffer == nil {
			cs.buffer = bytes.NewBuffer(make([]byte, 0, contentLength))
		} else {
			cs.buffer.Reset()
		}
		n, err := cs.buffer.Write(b)
		if err != nil {
			cs.log.Error("Write", "err", err)
		}
		return n, err
	}
	<-cs.writeMoreCh
	if cs.offset != 0 || cs.bufLevel != 0 {
		cs.log.Warn("bad write levels", "url", cs.url, "offset", cs.offset, "bufLevel", cs.bufLevel)
	}
	nrWritten := 0
	for {
		n := copy(cs.buf, b[nrWritten:])
		cs.nrBytesCh <- n
		nrWritten += n
		if nrWritten == len(b) {
			break
		}
		<-cs.writeMoreCh // Wait for OK from reader
	}
	return len(b), nil
}

func (cs *cmafSource) WriteHeader(status int) {
	cs.log.Debug("Writer status", "status", status)
	cs.status = status
}

// Read reads data from the intermediate buffer.
// It is triggered by receiving a message on nrBytesCh
// with how many bytes are available.
// The receiver never returns 0 bytes, except together
// with io.EOF.
func (cs *cmafSource) Read(p []byte) (int, error) {
	if cs.offset >= cs.bufLevel {
		nrAvailable := <-cs.nrBytesCh // wait for more bytes
		cs.bufLevel = nrAvailable
		if cs.bufLevel < 0 {
			return 0, io.EOF
		}
		if cs.offset != 0 {
			cs.log.Warn("Read", "url", cs.url, "offset is not zero", cs.offset)
		}
	}
	n := copy(p, cs.buf[cs.offset:cs.bufLevel])
	cs.offset += n
	if cs.offset == cs.bufLevel {
		cs.offset = 0
		cs.bufLevel = 0
		cs.writeMoreCh <- struct{}{}
	}
	return n, nil
}

type parentBox interface {
	AddChild(b mp4.Box)
}

func setInitProps(initSeg *mp4.InitSegment, rd cmafRepData, startTimeS int64) {
	moov := initSeg.Moov
	moov.Mvhd.SetCreationTimeS(startTimeS)
	moov.Mvhd.SetModificationTimeS(startTimeS)
	trak := moov.Trak
	trak.Tkhd.SetCreationTimeS(startTimeS)
	trak.Tkhd.SetModificationTimeS(startTimeS)
	trak.Mdia.Mdhd.SetCreationTimeS(startTimeS)
	trak.Mdia.Mdhd.SetModificationTimeS(startTimeS)
	stsd := trak.Mdia.Minf.Stbl.Stsd
	btrt := stsd.GetBtrt()
	if btrt == nil {
		sampleEntry, ok := stsd.Children[0].(parentBox)
		if ok {
			sampleEntry.AddChild(&mp4.BtrtBox{BufferSizeDB: 0, MaxBitrate: rd.bandWidth, AvgBitrate: rd.bandWidth})
		}
	}
	if len(rd.roles) > 0 {
		udta := mp4.UdtaBox{}
		for _, role := range rd.roles {
			kind := mp4.KindBox{}
			kind.SchemeURI = "urn:mpeg:dash:role:2011"
			kind.Value = role.Value
			udta.AddChild(&kind)
		}
		trak.AddChild(&udta)
	}
}

func setRawInitProps(rawInit []byte, rd cmafRepData, startTimeS int64) (newRawInit []byte, err error) {
	sr := bits.NewFixedSliceReader(rawInit)
	f, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return nil, err
	}
	initSeg := f.Init
	setInitProps(initSeg, rd, startTimeS)
	sw := bits.NewFixedSliceWriter(int(initSeg.Size()))
	err = initSeg.EncodeSW(sw)
	if err != nil {
		return nil, err
	}
	return sw.Bytes(), nil
}
