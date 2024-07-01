package app

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// CmafIngesterRequest represents the CMAF ingest start request.
type CmafIngesterSetup struct {
	User      string `json:"user,omitempty" doc:"User name for basic auth" example:""`
	PassWord  string `json:"password,omitempty" doc:"Password for basic auth" example:""`
	Dest      string `json:"destination" doc:"Destination URL path for segments" example:"https://server.com/ingest"`
	URL       string `json:"livesimURL" doc:"Full livesimURL without scheme and host" example:"/livesim2/segtimeline_1/testpic_2s/Manifest.mpd"`
	TestNowMS *int   `json:"testNowMS,omitempty" doc:"Test: start time for step-wise sending"`
	Duration  *int   `json:"duration,omitempty" doc:"Duration in seconds for the CMAF ingest session" example:"60"`
}

type CmafIngesterCreateRequest struct {
	Body CmafIngesterSetup `json:"body"`
}

type CmafIngestCreateResponse struct {
	Body struct {
		Dest string `json:"destination" doc:"Destination URL for the CMAF ingest"`
		URL  string `json:"livesim-url" doc:"livesim2 URL including /livesim2/ prefix"`
		ID   string `json:"id" doc:"Unique ID for the CMAF ingest"`
	}
}

type CmafIngestInfoResponse struct {
	Body struct {
		Dest string `json:"destination" doc:"Destination URL for the CMAF ingest"`
		URL  string `json:"livesim-url" doc:"livesim2 URL including /livesim2/ prefix"`
		ID   string `json:"id" doc:"Unique ID for the CMAF ingest"`
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
		Dest string `json:"destination" doc:"Destination URL for the CMAF ingest"`
		URL  string `json:"livesim-url" doc:"livesim2 URL including /livesim2/ prefix"`
		ID   string `json:"id" doc:"Unique ID for the CMAF ingest"`
	}
}

func createCmafIngesterHdlr(s *Server) func(ctx context.Context, cfi *CmafIngesterCreateRequest) (*CmafIngestCreateResponse, error) {
	return func(ctx context.Context, cfi *CmafIngesterCreateRequest) (*CmafIngestCreateResponse, error) {
		nr, err := s.cmafMgr.NewCmafIngester(cfi.Body)
		if err == nil {
			s.cmafMgr.startIngester(nr)
		}
		resp := &CmafIngestCreateResponse{}
		resp.Body.Dest = cfi.Body.Dest
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
		_, ok := s.cmafMgr.ingesters[uint64(id)]
		if !ok {
			return nil, huma.Error404NotFound(fmt.Sprintf("CMAF ingest %s not found", input.Id))
		}
		resp := &CmafIngestInfoResponse{}
		resp.Body.ID = fmt.Sprintf("Info about %s!", input.Id)
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

func createRouteAPI(s *Server) func(r chi.Router) {
	return func(r chi.Router) {
		config := huma.DefaultConfig("Livesim2 API for sessions", "1.0.0")
		config.Servers = []*huma.Server{
			{URL: "/api"},
		}
		config.Info.Description = `The first use case is for generating CMAF ingest streams which are
		sent to a specified URL. These streams can be used to test CMAF ingest receivers.`

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
			Description: "In testing mode, send the next instance of all segment with the given ID.",
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
	}
}
