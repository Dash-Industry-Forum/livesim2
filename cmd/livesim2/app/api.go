package app

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// CmafIngesterRequest represents the CMAF ingest start request.
type CmafIngesterSetup struct {
	User     string `json:"user,omitempty" doc:"User name for basic auth" example:""`
	PassWord string `json:"password,omitempty" doc:"Password for basic auth" example:""`
	DestRoot string `json:"destRoot" doc:"Destination URL root for assets" example:"https://server.com/upload"`
	DestName string `json:"destName" doc:"Destination name for asset" example:"testpic_ingest"`
	//nolint:lll
	URL       string `json:"livesimURL" doc:"Full livesimURL without scheme and host" example:"/livesim2/segtimeline_1/testpic_2s/Manifest.mpd"`
	TestNowMS *int   `json:"testNowMS,omitempty" doc:"Test: start time for step-wise sending"`
	Duration  *int   `json:"duration,omitempty" doc:"Duration in seconds for the CMAF ingest session" example:"60"`
	//nolint:lll
	StreamsURLs bool `json:"streamsURLs,omitempty" doc:"Use streams URLs likes Streams(video.cmfv) instead of individual segment URLs" example:"false"`
}

type CmafIngesterCreateRequest struct {
	Body CmafIngesterSetup `json:"body"`
}

type CmafIngestCreateResponse struct {
	Body struct {
		DestRoot string `json:"destRoot" doc:"Destination root URL for the CMAF ingest"`
		DestName string `json:"destName" doc:"Destination name for the CMAF ingest"`
		URL      string `json:"livesim-url" doc:"livesim2 URL including /livesim2/ prefix"`
		ID       string `json:"id" doc:"Unique ID for the CMAF ingest"`
	}
}

type CmafIngestInfoResponse struct {
	Body struct {
		DestRoot string `json:"destRoot" doc:"Destination root URL for the CMAF ingest"`
		DestName string `json:"destName" doc:"Destination name for the CMAF ingest"`
		URL      string `json:"livesim-url" doc:"livesim2 URL including /livesim2/ prefix"`
		ID       string `json:"id" doc:"Unique ID for the CMAF ingest"`
		Report   string `json:"report" doc:"Report for the CMAF ingest"`
	}
}

type CmafIngestStepResponse struct {
	Body struct {
		Nr   string `json:"nr" doc:"Segment number of segment sent"`
		Time string `json:"time" doc:"Time (in ms) of segment sent (if defined)"`
		ID   string `json:"id" doc:"Unique ID for the CMAF ingest"`
	}
}

type CmafIngestDeleteResponse struct {
	Body struct {
		DestRoot string `json:"destRoot" doc:"Destination root URL for the CMAF ingest"`
		DestName string `json:"destName" doc:"Destination name for the CMAF ingest"`
		URL      string `json:"livesim-url" doc:"livesim2 URL including /livesim2/ prefix"`
		ID       string `json:"id" doc:"Unique ID for the CMAF ingest"`
	}
}

func createCmafIngesterHdlr(s *Server) func(ctx context.Context, cfi *CmafIngesterCreateRequest) (*CmafIngestCreateResponse, error) {
	return func(ctx context.Context, cfi *CmafIngesterCreateRequest) (*CmafIngestCreateResponse, error) {
		nr, err := s.cmafMgr.NewCmafIngester(cfi.Body)
		if err == nil {
			s.cmafMgr.startIngester(nr)
		}
		resp := &CmafIngestCreateResponse{}
		resp.Body.DestRoot = cfi.Body.DestRoot
		resp.Body.DestName = cfi.Body.DestName
		resp.Body.URL = cfi.Body.URL
		resp.Body.ID = fmt.Sprintf("%d", nr)
		return resp, err
	}
}

type idInput struct {
	Id string `path:"id" maxLength:"32" example:"1234" doc:"Unique ID for the CMAF ingest"`
}

func createGetCmafIngesterInfoHdlr(s *Server) func(ctx context.Context, input *idInput) (*CmafIngestInfoResponse, error) {
	return func(ctx context.Context, input *idInput) (*CmafIngestInfoResponse, error) {
		id, err := strconv.Atoi(input.Id)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("Invalid ID: %s", input.Id))
		}
		ing, ok := s.cmafMgr.ingesters[uint64(id)]
		if !ok {
			return nil, huma.Error404NotFound(fmt.Sprintf("CMAF ingest %s not found", input.Id))
		}
		resp := &CmafIngestInfoResponse{}
		resp.Body.DestRoot = ing.destRoot
		resp.Body.DestName = ing.destName
		resp.Body.URL = ing.url
		resp.Body.ID = input.Id
		resp.Body.Report = strings.Join(ing.report, "\n")
		return resp, nil
	}
}

func createStepCmafIngesterHdlr(s *Server) func(ctx context.Context, input *idInput) (*CmafIngestStepResponse, error) {
	return func(ctx context.Context, input *idInput) (*CmafIngestStepResponse, error) {
		id, err := strconv.Atoi(input.Id)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("Invalid ID: %s", input.Id))
		}
		ci, ok := s.cmafMgr.ingesters[uint64(id)]
		if !ok {
			return nil, huma.Error404NotFound(fmt.Sprintf("CMAF ingest %s not found", input.Id))
		}
		ci.triggerNextSegment()
		resp := &CmafIngestStepResponse{}
		resp.Body.ID = fmt.Sprintf("Stepped %s!", input.Id)
		return resp, nil
	}
}

func createDeleteCmafIngesterHdlr(s *Server) func(ctx context.Context, input *idInput) (*CmafIngestDeleteResponse, error) {
	return func(ctx context.Context, input *idInput) (*CmafIngestDeleteResponse, error) {
		id, err := strconv.Atoi(input.Id)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("Invalid ID: %s", input.Id))
		}
		ci, ok := s.cmafMgr.ingesters[uint64(id)]
		if !ok {
			return nil, huma.Error404NotFound(fmt.Sprintf("CMAF ingest %s not found", input.Id))
		}
		if ci.state == ingesterStateRunning {
			ci.mgr.cancels[uint64(id)]()
		}

		s.cmafMgr.cancels[uint64(id)]()
		resp := &CmafIngestDeleteResponse{}
		resp.Body.ID = fmt.Sprintf("Deleted %s!", input.Id)
		return resp, nil
	}
}

// SgaiSessionResponse is the OpenAPI response for a single SGAI session.
type SgaiSessionResponse struct {
	Body struct {
		Session SgaiSession `json:"session" doc:"Recorded ad decisions and beacons for the session"`
	}
}

// SgaiSessionListResponse is the OpenAPI response listing active SGAI sessions (no timelines).
type SgaiSessionListResponse struct {
	Body struct {
		Sessions []SgaiSession `json:"sessions" doc:"Active sessions, most-recently-active first"`
	}
}

type sgaiSidInput struct {
	Sid string `path:"sid" maxLength:"256" example:"alice" doc:"Session id (sessionId/sid carried on the stream)"`
}

// SgaiClearResponse is the OpenAPI response for clearing SGAI session status.
type SgaiClearResponse struct {
	Body struct {
		Cleared int `json:"cleared" doc:"Number of sessions removed"`
	}
}

func createClearSgaiSessionsHdlr(s *Server) func(ctx context.Context, input *struct{}) (*SgaiClearResponse, error) {
	return func(ctx context.Context, input *struct{}) (*SgaiClearResponse, error) {
		resp := &SgaiClearResponse{}
		if s.sgaiSessions != nil {
			resp.Body.Cleared = s.sgaiSessions.Clear()
		}
		return resp, nil
	}
}

func createClearSgaiSessionHdlr(s *Server) func(ctx context.Context, input *sgaiSidInput) (*SgaiClearResponse, error) {
	return func(ctx context.Context, input *sgaiSidInput) (*SgaiClearResponse, error) {
		resp := &SgaiClearResponse{}
		if s.sgaiSessions != nil && s.sgaiSessions.ClearSession(input.Sid) {
			resp.Body.Cleared = 1
		}
		return resp, nil
	}
}

// AdCatalogResponse is the OpenAPI response listing the available ad creatives.
type AdCatalogResponse struct {
	Body struct {
		Ads []adEntry `json:"ads" doc:"Available ad creatives with interest tags and durations"`
	}
}

func createGetSgaiAdsHdlr(s *Server) func(ctx context.Context, input *struct{}) (*AdCatalogResponse, error) {
	return func(ctx context.Context, input *struct{}) (*AdCatalogResponse, error) {
		resp := &AdCatalogResponse{}
		resp.Body.Ads = s.adCatalog().ads
		if resp.Body.Ads == nil {
			resp.Body.Ads = []adEntry{}
		}
		return resp, nil
	}
}

func createGetSgaiSessionHdlr(s *Server) func(ctx context.Context, input *sgaiSidInput) (*SgaiSessionResponse, error) {
	return func(ctx context.Context, input *sgaiSidInput) (*SgaiSessionResponse, error) {
		if s.sgaiSessions == nil {
			return nil, huma.Error404NotFound("session tracking not enabled")
		}
		sess, ok := s.sgaiSessions.Get(input.Sid)
		if !ok {
			return nil, huma.Error404NotFound(fmt.Sprintf("no SGAI activity for session %q", input.Sid))
		}
		resp := &SgaiSessionResponse{}
		resp.Body.Session = *sess
		return resp, nil
	}
}

func createListSgaiSessionsHdlr(s *Server) func(ctx context.Context, input *struct{}) (*SgaiSessionListResponse, error) {
	return func(ctx context.Context, input *struct{}) (*SgaiSessionListResponse, error) {
		resp := &SgaiSessionListResponse{}
		if s.sgaiSessions != nil {
			resp.Body.Sessions = s.sgaiSessions.List()
		}
		if resp.Body.Sessions == nil {
			resp.Body.Sessions = []SgaiSession{}
		}
		return resp, nil
	}
}

func createRouteAPI(s *Server) func(r chi.Router) {
	return func(r chi.Router) {
		config := huma.DefaultConfig("Livesim2 API for sessions", "1.0.0")
		config.Servers = []*huma.Server{
			{URL: "/api"},
		}
		config.Info.Description = `The first use case is for generating CMAF ingest streams which are
		sent to a specified URL. These streams can be used to test CMAF ingest receivers.

		The second use case is Server-Guided Ad Insertion (SGAI): list the available ad
		creatives with their interest tags and durations (/sgai/ads), and follow the ad
		decisions and impression/quartile beacons recorded per session (/sgai/sessions),
		as shown live on the /sgai/session_status page.`

		api := humachi.New(r, config)

		// Register POST /cmaf-ingests that creates a new CMAF-Ingest source
		huma.Register(api, huma.Operation{
			OperationID:   "create-cmaf-ingest",
			Method:        http.MethodPost,
			Path:          "/cmaf-ingests",
			Summary:       "Create a CMAF ingest stream",
			Tags:          []string{"CMAF-ingest"},
			DefaultStatus: http.StatusCreated,
			Errors:        []int{404, 409, 410},
		}, createCmafIngesterHdlr(s))

		// Register GET /cmaf-ingests/{id}
		huma.Register(api, huma.Operation{
			OperationID: "get-cmaf-ingest",
			Method:      http.MethodGet,
			Path:        "/cmaf-ingests/{id}",
			Summary:     "Get information about a CMAF ingest stream",
			Description: "Get information about CMAF ingest stream with the given ID.",
			Tags:        []string{"CMAF-ingest"},
			Errors:      []int{404, 410},
		}, createGetCmafIngesterInfoHdlr(s))

		// Register GET /cmaf-ingests/{id}/step
		huma.Register(api, huma.Operation{
			OperationID: "step-cmaf-ingest",
			Method:      http.MethodGet,
			Path:        "/cmaf-ingests/{id}/step",
			Summary:     "Step a CMAF ingest stream one step (for testing)",
			//nolint: lll
			Description: "In testing mode (triggered by setting timeNowMS in creation), send the next segment of all tracks for the given stream ID.",
			Tags:        []string{"CMAF-ingest"},
			Errors:      []int{404, 410},
		}, createStepCmafIngesterHdlr(s))

		// Register DELETE /cmaf-ingests/{id}
		huma.Register(api, huma.Operation{
			OperationID: "delete-cmaf-ingest",
			Method:      http.MethodDelete,
			Path:        "/cmaf-ingests/{id}",
			Summary:     "Stop and delete a CMAF ingest stream",
			Description: "Stop a CMAF request and get back a report.",
			Tags:        []string{"CMAF-ingest"},
			Errors:      []int{404, 410},
		}, createDeleteCmafIngesterHdlr(s))

		// Register GET /sgai/sessions — list active SGAI sessions (decisions + beacons).
		huma.Register(api, huma.Operation{
			OperationID: "list-sgai-sessions",
			Method:      http.MethodGet,
			Path:        "/sgai/sessions",
			Summary:     "List active SGAI sessions",
			//nolint: lll
			Description: "List the session ids with recorded Server-Guided Ad Insertion activity (ad decisions and impression beacons), most-recently-active first. Timelines are omitted; fetch a single session for its events.",
			Tags:        []string{"SGAI"},
		}, createListSgaiSessionsHdlr(s))

		// Register POST /sgai/sessions/clear — wipe all recorded sessions for a clean start.
		// POST (not DELETE) so a cross-origin call from a browser page is a CORS "simple
		// request" and needs no preflight (the API does not answer OPTIONS preflights).
		huma.Register(api, huma.Operation{
			OperationID: "clear-sgai-sessions",
			Method:      http.MethodPost,
			Path:        "/sgai/sessions/clear",
			Summary:     "Clear all SGAI session status",
			//nolint: lll
			Description: "Remove all recorded Server-Guided Ad Insertion session activity (ad decisions and impression beacons) to get a clean slate.",
			Tags:        []string{"SGAI"},
		}, createClearSgaiSessionsHdlr(s))

		// Register POST /sgai/sessions/{sid}/clear — wipe one session's status. POST (not
		// DELETE) for the same no-preflight reason as the clear-all route above.
		huma.Register(api, huma.Operation{
			OperationID: "clear-sgai-session",
			Method:      http.MethodPost,
			Path:        "/sgai/sessions/{sid}/clear",
			Summary:     "Clear one SGAI session's status",
			//nolint: lll
			Description: "Remove the recorded ad decisions and impression beacons for a single session id, to reset just that session (e.g. one viewer).",
			Tags:        []string{"SGAI"},
		}, createClearSgaiSessionHdlr(s))

		// Register GET /sgai/sessions/{sid} — one session's decisions + beacon timeline.
		huma.Register(api, huma.Operation{
			OperationID: "get-sgai-session",
			Method:      http.MethodGet,
			Path:        "/sgai/sessions/{sid}",
			Summary:     "Get SGAI activity for a session",
			//nolint: lll
			Description: "Get the recorded ad decisions (pods, with interest steering) and impression beacons for a session id. This is how to observe SGAI activity on a public livesim2 deployment. Backs the live page at /sgai/session_status?sid=<sid>.",
			Tags:        []string{"SGAI"},
			Errors:      []int{404},
		}, createGetSgaiSessionHdlr(s))

		// Register GET /sgai/ads — the ad catalog (creatives with tags + durations).
		huma.Register(api, huma.Operation{
			OperationID: "list-sgai-ads",
			Method:      http.MethodGet,
			Path:        "/sgai/ads",
			Summary:     "List the available ad creatives",
			//nolint: lll
			Description: "The ad catalog: the available Single-Period-Static ad creatives with their interest tags and durations, as used for ad-pod selection (interest steering + duration fit).",
			Tags:        []string{"SGAI"},
		}, createGetSgaiAdsHdlr(s))
	}
}
