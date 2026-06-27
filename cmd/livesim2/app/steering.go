// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	m "github.com/Eyevinn/dash-mpd/mpd"
)

// DASH Content Steering (ISO/IEC 23009-1 6th ed. §K.3.6, ETSI TS 103 998).
//
// A single livesim2 server advertises two or more "CDNs" (service locations) that all point
// back to itself, plus a root <ContentSteering> element referencing a steering endpoint on
// this same server. A DASH client (e.g. dash.js) polls the steering endpoint, which returns a
// steering manifest with a PATHWAY-PRIORITY ordering and a TTL. By changing that ordering the
// server makes the client switch "CDN"; the per-CDN segment requests are tracked per session
// (see steering_sessions.go) and exposed via the API and the live status page.
//
// The CDNs are distinguished by a path token baked into each BaseURL (cdn_<name>/sid_<id>/...)
// so every segment request is attributable to a (session, service-location) pair — a relative
// segment reference does not inherit a BaseURL query, so the identity must live in the path.
//
// An optional csid_<group> path token groups several sessions under one shared steering decision:
// switching the group moves every member, while per-session segment counts and the _DASH_pathway
// verification stay individual. Streams without a csid behave as a group of one (the session owns
// its own decision, as before). The csid is a path token for the same reason as sid: it must ride
// along on relative segment references, which do not inherit a query.

const (
	steeringModeRotate  = "rotate"  // priority rotates every TTL (wall-clock based, stateless)
	steeringModeTrigger = "trigger" // priority is held at the default order until a switch is triggered via the API/monitor (default)

	steeringDefaultTTLS = 300 // default steering-manifest TTL in seconds (DASH/HLS recommended)
	steeringManifestVer = 1   // DASH steering manifest VERSION (dash.js requires 1)
)

// SteeringConfig configures DASH Content Steering for a live stream. It is parsed from the
// "steer" URL option and is also reconstructed by the steering endpoint from its own URL, so
// the steering server is stateless with respect to per-stream configuration.
type SteeringConfig struct {
	CDNs             []string `json:"CDNs"`                       // service-location names, in declared order (>= 2)
	TTL              int      `json:"TTL"`                        // steering-manifest TTL in seconds
	Mode             string   `json:"Mode"`                       // steeringModeRotate or steeringModeTrigger
	QueryBeforeStart bool     `json:"QueryBeforeStart,omitempty"` // ContentSteering@queryBeforeStart
	Default          string   `json:"Default,omitempty"`          // initial top service location (default: first CDN)
}

// CreateSteeringConfig parses the value of a "steer" URL option.
//
// Grammar: <name1>,<name2>[,<name3>...][;key=val;...]
// keys: ttl=<seconds> (default 300), mode=rotate|trigger (default trigger),
//
//	qbs=<0|1> (queryBeforeStart, default 0), default=<name> (initial top, default first).
//
// Examples:
//
//	alpha,beta                      => two CDNs, held on alpha until a switch is triggered (API/monitor)
//	alpha,beta;default=beta         => held on beta until a switch is triggered
//	cdnA,cdnB,cdnC;mode=rotate;ttl=20 => priority rotates one step every 20 s, hands-off
//	east,west;ttl=10;default=west   => west served first
func CreateSteeringConfig(val string) (*SteeringConfig, error) {
	if val == "" {
		return nil, fmt.Errorf("empty steer config")
	}
	if hasExtraSpaces(val) {
		return nil, fmt.Errorf("steer config %q has extra spaces", val)
	}
	cfg := &SteeringConfig{
		TTL:  steeringDefaultTTLS,
		Mode: steeringModeTrigger,
	}
	parts := strings.Split(val, ";")
	for name := range strings.SplitSeq(parts[0], ",") {
		if !isValidServiceLocation(name) {
			return nil, fmt.Errorf("steer service location %q: must be non-empty and use only [A-Za-z0-9._-]", name)
		}
		cfg.CDNs = append(cfg.CDNs, name)
	}
	if dupServiceLocation(cfg.CDNs) {
		return nil, fmt.Errorf("steer config %q has duplicate service locations", parts[0])
	}
	for _, kv := range parts[1:] {
		key, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("steer param %q must be key=val", kv)
		}
		switch key {
		case "ttl":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("steer ttl %q: must be a positive integer", v)
			}
			cfg.TTL = n
		case "mode":
			switch v {
			case steeringModeRotate, steeringModeTrigger:
				cfg.Mode = v
			default:
				return nil, fmt.Errorf("steer mode %q: must be rotate or trigger", v)
			}
		case "qbs":
			switch v {
			case "1", "true":
				cfg.QueryBeforeStart = true
			case "0", "false":
				cfg.QueryBeforeStart = false
			default:
				return nil, fmt.Errorf("steer qbs %q: must be 0 or 1", v)
			}
		case "default":
			cfg.Default = v
		default:
			return nil, fmt.Errorf("unknown steer param %q", key)
		}
	}
	if len(cfg.CDNs) < 2 {
		return nil, fmt.Errorf("steer config %q must list at least two service locations", val)
	}
	if cfg.Default != "" && !slices.Contains(cfg.CDNs, cfg.Default) {
		return nil, fmt.Errorf("steer default %q is not one of the service locations %v", cfg.Default, cfg.CDNs)
	}
	return cfg, nil
}

// ParseSteeringConfig parses a steer option value, accumulating any error on the converter.
func (s *strConvAccErr) ParseSteeringConfig(key, val string) *SteeringConfig {
	if s.err != nil {
		return nil
	}
	cfg, err := CreateSteeringConfig(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return cfg
}

// isValidServiceLocation reports whether name is a safe service-location/CDN token. It is used
// both as a serviceLocation attribute and as a cdn_<name> path token, so it must be a clean
// path/identifier segment.
func isValidServiceLocation(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func dupServiceLocation(ss []string) bool {
	seen := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		if _, ok := seen[s]; ok {
			return true
		}
		seen[s] = struct{}{}
	}
	return false
}

// defaultOrder returns the CDN list with Default (if set) moved to the front, keeping the
// declared order otherwise. This is the priority used before any switch and the order advertised
// in ContentSteering@defaultServiceLocation (so the client's pick is deterministic at start-up).
func (c *SteeringConfig) defaultOrder() []string {
	out := make([]string, 0, len(c.CDNs))
	if c.Default != "" {
		out = append(out, c.Default)
	}
	for _, name := range c.CDNs {
		if name != c.Default {
			out = append(out, name)
		}
	}
	return out
}

// rotatePriority returns the priority order for rotate mode at the given wall-clock time. The
// rotation is stateless and time-bucketed (bucket = floor(unixSeconds / TTL) mod N), so every
// client rotates in lockstep, deterministically, switching to the next service location at each
// TTL boundary with no API calls.
func (c *SteeringConfig) rotatePriority(nowUnixS int64) []string {
	base := c.defaultOrder()
	n := len(base)
	if n == 0 {
		return base
	}
	bucket := int((nowUnixS / int64(c.TTL)) % int64(n))
	if bucket < 0 {
		bucket += n
	}
	out := make([]string, n)
	for i := range n {
		out[i] = base[(i+bucket)%n]
	}
	return out
}

// SteeringManifest is the DASH steering manifest (ETSI TS 103 998 / DASH-IF) returned by the
// steering endpoint and parsed by the client. dash.js requires VERSION == 1.
type SteeringManifest struct {
	Version         int      `json:"VERSION"`
	TTL             int      `json:"TTL"`
	ReloadURI       string   `json:"RELOAD-URI,omitempty"`
	PathwayPriority []string `json:"PATHWAY-PRIORITY"`
}

// tokenFromParts returns the first URL path token with the given prefix, verbatim, or "" if none.
func tokenFromParts(parts []string, prefix string) string {
	for _, p := range parts {
		if strings.HasPrefix(p, prefix) {
			return p
		}
	}
	return ""
}

// steerTokenFromParts returns the "steer_..." URL path token verbatim from the parsed URL parts
// (so the steering endpoint URL re-uses the exact stream configuration), or "" if none.
func steerTokenFromParts(parts []string) string {
	return tokenFromParts(parts, "steer_")
}

// steeringServerPath returns the path tokens that identify the steering configuration and, if set,
// the content-steering group on the steering endpoint URL: "csid_<grp>/steer_<spec>" when a group is
// present, otherwise just "steer_<spec>". Taken verbatim from the parsed stream URL parts so the
// steering server stays stateless about the stream configuration.
func steeringServerPath(parts []string) string {
	steer := steerTokenFromParts(parts)
	if csid := tokenFromParts(parts, "csid_"); csid != "" {
		return csid + "/" + steer
	}
	return steer
}

// steeringBaseURL builds the absolute BaseURL (a directory, ending in "/") for one service
// location: the stream's directory with cdn_<loc>/sid_<id> injected right after the /livesim2
// mount, so every segment fetched through it carries the (service-location, session) identity.
// URLParts is ["", "livesim2", <cfg tokens...>, <asset dirs...>, <mpd file>].
func steeringBaseURL(cfg *ResponseConfig, loc, sid string) string {
	dirParts := cfg.URLParts[1 : len(cfg.URLParts)-1] // drop leading "" and trailing MPD filename
	var b strings.Builder
	b.WriteString(cfg.Host)
	b.WriteByte('/')
	b.WriteString(dirParts[0]) // "livesim2"
	b.WriteString("/cdn_")
	b.WriteString(loc)
	b.WriteString("/sid_")
	b.WriteString(url.PathEscape(sid))
	for _, p := range dirParts[1:] {
		b.WriteByte('/')
		b.WriteString(p)
	}
	b.WriteByte('/')
	return b.String()
}

// steeringServerURL builds the absolute ContentSteering server URL (the steering endpoint),
// carrying the session id. pathPart is the "steer_<spec>" token, optionally prefixed with a
// "csid_<grp>/" group token. The client appends _DASH_pathway/_DASH_throughput itself and
// preserves this query (DASH-IF / ETSI TS 103 998).
func steeringServerURL(host, pathPart, sid string) string {
	return fmt.Sprintf("%s/steering/%s?sessionId=%s", host, pathPart, url.QueryEscape(sid))
}

// addContentSteering injects the per-CDN BaseURLs (with serviceLocation) into the period and
// the root <ContentSteering> element into the MPD. sid is the client-supplied session id.
func addContentSteering(mpd *m.MPD, period *m.Period, cfg *ResponseConfig) {
	if cfg.Steer == nil {
		return
	}
	sid := cfg.SteerSessionID
	if sid == "" {
		sid = "anon"
	}
	for _, loc := range cfg.Steer.CDNs {
		b := m.NewBaseURL(steeringBaseURL(cfg, loc, sid))
		b.ServiceLocation = loc
		period.BaseURLs = append(period.BaseURLs, b)
	}
	mpd.ContentSteering = &m.ContentSteeringType{
		DefaultServiceLocation: strings.Join(cfg.Steer.defaultOrder(), " "),
		QueryBeforeStart:       cfg.Steer.QueryBeforeStart,
		Value:                  steeringServerURL(cfg.Host, steeringServerPath(cfg.URLParts), sid),
	}
}
